// Package api is the HTTP surface: the native beat endpoint, the health endpoint
// that reports our own loop ages, and a plain monitors wall. The Sentry-compatible
// envelope endpoint arrives with M2.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/clients"
	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/alert"
	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/events"
	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/metrics"
	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/monitor"
	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/store"
)

// SessionChecker lets the JSON endpoints accept a UI session without importing
// the web package (which imports this one's siblings).
type SessionChecker interface {
	Authenticated(*http.Request) bool
}

type Server struct {
	db       *store.DB
	events   *events.Store
	apiToken string
	engine   *monitor.Engine
	detector *monitor.Detector
	alerter  *alert.Alerter
	log      *slog.Logger
	version  string
	baseURL  string
	started  time.Time
	session  SessionChecker
	envLimit envelopeLimiter
	// audit is the buffered path to audit_log; see internal/api/ops.go.
	audit chan store.AuditEntry
}

// New builds the HTTP surface. apiToken is the operator credential the UI presents;
// when empty the admin endpoints fall back to accepting any ingest key, which is a
// development convenience the caller is expected to warn about loudly.
func New(db *store.DB, e *monitor.Engine, d *monitor.Detector, a *alert.Alerter, log *slog.Logger, version, apiToken, baseURL string, session SessionChecker) *Server {
	s := &Server{
		db: db, events: events.New(db), engine: e, detector: d, alerter: a, log: log,
		version: version, apiToken: apiToken, baseURL: strings.TrimRight(baseURL, "/"),
		started: time.Now(), session: session,
	}
	s.startAuditWriter()
	s.registerGauges()
	return s
}

func presentedKey(r *http.Request) string {
	if k := r.Header.Get("X-BSIO-Key"); k != "" {
		return k
	}
	if k := r.URL.Query().Get("key"); k != "" {
		return k
	}
	// Bearer is how every Sentry client sends its credential, Grafana's datasource
	// included. Accepting it here lets an ingest key be that credential, so a
	// dashboard need not hold the operator token: hasAPIToken still compares against
	// the operator token specifically, so a bearer ingest key buys reads and nothing
	// else.
	if v := r.Header.Get("Authorization"); strings.HasPrefix(v, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(v, "Bearer "))
	}
	return ""
}

// hasAPIToken reports whether the caller presented the operator token. Compared in
// constant time so a wrong guess leaks nothing through timing.
func (s *Server) hasAPIToken(r *http.Request) bool {
	if s.apiToken == "" {
		return false
	}
	got := presentedKey(r)
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.apiToken)) == 1
}

// mayAdminister gates everything that changes state: creating and deleting apps,
// muting monitors. An ingest key proves a service can report its own heartbeats — it
// must not also let whoever holds it reshape or destroy the monitoring estate.
//
// With no --api-token configured it degrades to mayRead so a fresh install is usable;
// main() warns about that at startup, the same way it does for the default password.
func (s *Server) mayAdminister(r *http.Request) bool {
	if s.session != nil && s.session.Authenticated(r) {
		return true
	}
	if s.hasAPIToken(r) {
		return true
	}
	return s.apiToken == "" && s.authorized(r)
}

// authorized gates reads. Any valid ingest key is accepted so a service can check
// its own state from a script without a browser.
func (s *Server) authorized(r *http.Request) bool {
	if s.session != nil && s.session.Authenticated(r) {
		return true
	}
	if s.hasAPIToken(r) {
		return true
	}
	key := presentedKey(r)
	if key == "" {
		return false
	}
	// An auth token is recognisable by its prefix, so the right lookup is chosen by
	// shape rather than by trying both tables on every request. This is the credential
	// a dashboard should hold: named, revocable, and unable to administer anything.
	if strings.HasPrefix(key, store.TokenPrefix) {
		ok, err := s.db.APITokenValid(r.Context(), key)
		if err != nil {
			s.log.Error("token lookup failed", "err", err)
		}
		return ok
	}
	id, err := s.db.ProjectIDForKey(r.Context(), key)
	return err == nil && id != 0
}

// Handler wires the machine-facing endpoints. The operator UI is registered by
// internal/web onto the same mux, which owns "/" and everything under it.
func (s *Server) Handler(ui interface{ Routes(*http.ServeMux) }) http.Handler {
	mux := http.NewServeMux()
	// GET is accepted alongside POST so a shell loop can beat with a bare curl.
	mux.HandleFunc("POST /api/0/beat/{slug}", s.handleBeat)
	mux.HandleFunc("GET /api/0/beat/{slug}", s.handleBeat)
	mux.HandleFunc("GET /api/0/monitors", s.handleMonitors)
	// Read API for the Next.js frontend (internal/api/json.go).
	mux.HandleFunc("GET /api/0/overview", s.handleOverview)
	mux.HandleFunc("GET /api/0/monitors/{slug}", s.handleMonitorDetail)
	mux.HandleFunc("POST /api/0/monitors/{slug}/mute", s.handleMute)
	mux.HandleFunc("GET /api/0/incidents", s.handleIncidents)
	mux.HandleFunc("GET /api/0/apps", s.handleApps)
	mux.HandleFunc("POST /api/0/apps", s.handleCreateApp)
	mux.HandleFunc("GET /api/0/apps/{slug}", s.handleAppDetail)
	mux.HandleFunc("DELETE /api/0/apps/{slug}", s.handleDeleteApp)
	mux.HandleFunc("GET /api/0/apps/{slug}/series", s.handleProjectSeries)
	// Operability (internal/api/ops.go): per-project retention and the audit log.
	mux.HandleFunc("PUT /api/0/apps/{slug}/retention", s.handleSetRetention)
	mux.HandleFunc("GET /api/0/audit", s.handleAuditList)
	// Sentry-compatible ingest (D14, internal/api/envelope.go): where a stock
	// sentry-sdk DSN points. The project id in the path is numeric, so it can
	// never collide with the /api/0/ control surface.
	mux.HandleFunc("POST /api/{projectID}/envelope/{$}", s.handleEnvelope)
	mux.HandleFunc("POST /api/{projectID}/envelope", s.handleEnvelope)
	// Auth tokens for the read API (internal/api/tokens.go). Administering them takes
	// the operator token; the tokens themselves only read.
	mux.HandleFunc("GET /api/0/tokens", s.handleListTokens)
	mux.HandleFunc("POST /api/0/tokens", s.handleCreateToken)
	mux.HandleFunc("DELETE /api/0/tokens/{id}", s.handleRevokeToken)
	// Issue alert channel settings (internal/api/alerts.go).
	mux.HandleFunc("GET /api/0/alerts/teams", s.handleGetTeamsAlert)
	mux.HandleFunc("PUT /api/0/alerts/teams", s.handleSetTeamsAlert)
	mux.HandleFunc("GET /api/0/channels", s.handleListChannels)
	mux.HandleFunc("POST /api/0/channels", s.handleCreateChannel)
	// Test before save, for either scope: the body carries the config, so one
	// endpoint serves the global form and every project's.
	mux.HandleFunc("POST /api/0/channels/test", s.handleTestChannel)
	mux.HandleFunc("PUT /api/0/channels/{id}", s.handleUpdateChannel)
	mux.HandleFunc("DELETE /api/0/channels/{id}", s.handleDeleteChannel)
	// Project-level alerts: the channels one app routes to, the global channels it
	// has imported, and how patient it is about bursts.
	mux.HandleFunc("GET /api/0/apps/{slug}/alerts", s.handleProjectAlerts)
	mux.HandleFunc("PUT /api/0/apps/{slug}/alerts/patience", s.handleSetPatience)
	mux.HandleFunc("POST /api/0/apps/{slug}/channels", s.handleCreateProjectChannel)
	mux.HandleFunc("PUT /api/0/apps/{slug}/channels/{id}", s.handleUpdateProjectChannel)
	mux.HandleFunc("DELETE /api/0/apps/{slug}/channels/{id}", s.handleDeleteProjectChannel)
	mux.HandleFunc("POST /api/0/apps/{slug}/channels/import", s.handleImportChannel)
	mux.HandleFunc("DELETE /api/0/apps/{slug}/channels/import/{id}", s.handleUnimportChannel)
	// Error tracking (internal/api/errors.go). Ingest takes an ingest key like a beat;
	// reading takes a session or a key; resolving takes the operator token.
	mux.HandleFunc("POST /api/0/errors", s.handleIngestError)
	mux.HandleFunc("GET /api/0/issues", s.handleIssues)
	mux.HandleFunc("GET /api/0/issues/{id}", s.handleIssueDetail)
	mux.HandleFunc("POST /api/0/issues/{id}/resolve", s.handleResolveIssue)
	mux.HandleFunc("POST /api/0/issues/{id}/archive", s.handleArchiveIssue)
	mux.HandleFunc("POST /api/0/issues/{id}/priority", s.handleIssuePriority)
	mux.HandleFunc("DELETE /api/0/issues/{id}", s.handleDeleteIssue)
	mux.HandleFunc("GET /api/0/issues/{id}/events/{eventID}", s.handleIssueEvent)
	mux.HandleFunc("GET /api/0/issues/{id}/series", s.handleIssueSeries)
	mux.HandleFunc("GET /api/0/analytics", s.handleProjectAnalytics)
	mux.HandleFunc("GET /api/0/releases", s.handleReleases)
	mux.HandleFunc("GET /api/0/events/{uuid}/attachments", s.handleEventAttachments)
	mux.HandleFunc("GET /api/0/attachments/{id}", s.handleAttachmentDownload)
	// The Sentry Web API reads (internal/api/sentryweb.go): the endpoints Sentry's
	// own dashboards call, which is what makes Grafana's official Sentry datasource
	// work against us with no plugin of ours.
	s.sentryWebRoutes(mux)
	// The SDK sources, so a service can curl the client off the engine it reports to.
	clients.Routes(mux)
	// Health and readiness stay unauthenticated: a probe should not need a session,
	// and neither reveals anything beyond our own liveness.
	mux.HandleFunc("GET /-/health", s.handleHealth)
	mux.HandleFunc("GET /-/ready", s.handleReady)
	// Prometheus exposition. Unauthenticated like the probes — the labels carry no
	// project names or payloads — but meant for in-cluster scraping: the ingress
	// should not route /-/ paths to the world.
	mux.HandleFunc("GET /-/metrics", metrics.Handler())
	// Claim the rest of /api/0/ before the UI's "/" does. The UI's Require() only
	// knows session cookies, so without this an unrouted API path answers 401
	// "authentication required" to a caller holding a perfectly good token -- which
	// reads as a rejected credential when the truth is a wrong URL. A trailing slash
	// is enough to fall in here: /api/0/issues/2 is routed, /api/0/issues/ is not.
	// Registered patterns are more specific, so they still win.
	//
	// GET only, and not by preference: ingest claims POST /api/{projectID}/envelope,
	// and against that a bare POST /api/0/ is neither more nor less specific, which
	// ServeMux rejects as ambiguous at registration -- a panic at boot, not a 404.
	// Reads are where the confusion happens anyway; that is what a dashboard sends.
	mux.HandleFunc("GET /api/0/", s.handleAPINotFound)
	if ui != nil {
		ui.Routes(mux)
	}
	return s.observe(mux)
}

// observe is the outermost middleware: request counters for the metrics endpoint,
// the audit trail for control-plane mutations, and the slow/error log line.
func (s *Server) observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rec, r)
		httpRequests.With(codeClass(rec.code)).Inc()
		if auditable(r) {
			s.recordAudit(r, rec.code)
		}
		if rec.code >= 400 {
			s.log.Warn("http", "method", r.Method, "path", r.URL.Path,
				"status", rec.code, "dur_ms", time.Since(start).Milliseconds())
		}
	})
}

func (s *Server) handleBeat(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(r.PathValue("slug"))
	if slug == "" || len(slug) > 128 {
		writeErr(w, http.StatusBadRequest, "monitor slug must be 1-128 characters")
		return
	}

	key := r.Header.Get("X-BSIO-Key")
	if key == "" {
		key = r.URL.Query().Get("key")
	}
	if key == "" {
		writeErr(w, http.StatusUnauthorized, "missing ingest key (X-BSIO-Key header or ?key=)")
		return
	}
	projectID, err := s.db.ProjectIDForKey(r.Context(), key)
	if err != nil {
		s.log.Error("key lookup failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if projectID == 0 {
		writeErr(w, http.StatusForbidden, "unknown ingest key")
		return
	}

	q := r.URL.Query()
	req := monitor.BeatRequest{
		ProjectID:     projectID,
		Slug:          slug,
		Environment:   q.Get("env"),
		ExpectedEvery: parseSeconds(q.Get("every")),
		Grace:         parseSeconds(q.Get("grace")),
		StallWindow:   parseSeconds(q.Get("stall_window")),
	}
	if raw := q.Get("progress"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "progress must be an integer")
			return
		}
		req.Progress = &n
	}

	res, err := s.engine.Beat(r.Context(), req)
	if err != nil {
		s.log.Error("beat failed", "monitor", slug, "err", err)
		writeErr(w, http.StatusServiceUnavailable, "could not record beat")
		return
	}
	beatsTotal.Inc()
	writeJSON(w, http.StatusOK, res)
}

// handleMonitors accepts either a session cookie (the UI) or an ingest key, so
// scripts can read state without a browser but nothing is readable anonymously.
func (s *Server) handleMonitors(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "sign in, or pass an ingest key (X-BSIO-Key or ?key=)")
		return
	}
	views, err := s.engine.List(r.Context())
	if err != nil {
		s.log.Error("list monitors failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if views == nil {
		views = []monitor.View{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"monitors": views})
}

type health struct {
	Status   string         `json:"status"`
	Version  string         `json:"version"`
	UptimeS  int64          `json:"uptime_s"`
	Detector map[string]any `json:"detector"`
	Alerter  map[string]any `json:"alerter"`
	Database string         `json:"database"`
	Problems []string       `json:"problems,omitempty"`
}

// handleHealth reports the age of our own internal loops. A monitoring tool that
// answers 200 while its detector is wedged would be repeating the exact failure
// this project exists to catch, so a stale tick degrades the response.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	var problems []string

	dbState := "ok"
	if err := s.db.Ping(ctx); err != nil {
		dbState = "unreachable"
		problems = append(problems, "database unreachable: "+err.Error())
	}

	tickAge := s.detector.LastTickAge()
	maxAge := 3*s.detector.Interval() + 5*time.Second
	// A standby replica has an old (or absent) tick age *because it is healthy*:
	// another replica holds the detector lock and this one is correctly doing
	// nothing. Reporting that as a problem would page on the quiet replica of
	// every two-replica install.
	if s.detector.Leading() {
		if s.detector.Ticks() == 0 {
			if time.Since(s.started) > maxAge {
				problems = append(problems, "detector has never completed a tick")
			}
		} else if tickAge > maxAge {
			problems = append(problems, "detector tick is stale ("+tickAge.Round(time.Second).String()+")")
		}
	}
	// A detector that runs but fails is reported immediately, not after the
	// staleness timeout — otherwise this endpoint is green over a blind detector.
	// Lock-acquisition failures land here too, so a standby that *cannot even try*
	// is still visible.
	if f := s.detector.Failures(); f > 0 {
		problems = append(problems,
			"detector sweep failing ("+strconv.FormatInt(f, 10)+" consecutive): "+s.detector.LastError())
	}

	if dropped := s.alerter.Dropped(); dropped > 0 {
		problems = append(problems, "alert queue has dropped "+strconv.FormatInt(dropped, 10)+" events")
	}

	h := health{
		Status:  "ok",
		Version: s.version,
		UptimeS: int64(time.Since(s.started).Seconds()),
		Detector: map[string]any{
			"interval_s":           int64(s.detector.Interval().Seconds()),
			"ticks":                s.detector.Ticks(),
			"last_tick_age_s":      int64(tickAge.Seconds()),
			"consecutive_failures": s.detector.Failures(),
			"last_error":           s.detector.LastError(),
			// false = standing by; another replica holds the detector lock.
			"leader": s.detector.Leading(),
		},
		Alerter: map[string]any{
			"queue_depth": s.alerter.QueueDepth(),
			"sent":        s.alerter.Sent(),
			"suppressed":  s.alerter.Suppressed(),
			"failed":      s.alerter.Failed(),
			"dropped":     s.alerter.Dropped(),
		},
		Database: dbState,
		Problems: problems,
	}

	code := http.StatusOK
	if len(problems) > 0 {
		h.Status, code = "degraded", http.StatusServiceUnavailable
	}
	writeJSON(w, code, h)
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.db.Ping(ctx); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "database unreachable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func parseSeconds(raw string) time.Duration {
	if raw == "" {
		return 0
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return time.Duration(n) * time.Second
	}
	if d, err := time.ParseDuration(raw); err == nil {
		return d
	}
	return 0
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(body)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// handleAPINotFound answers any /api/0/ path no route claimed.
//
// It replies in the shape the caller is already parsing: Sentry's `detail` under the
// Sentry Web API, ours everywhere else. The Grafana datasource surfaces `detail`
// verbatim, so a mistyped URL says so in the operator's face instead of sending them
// to re-check a token that was never the problem.
func (s *Server) handleAPINotFound(w http.ResponseWriter, r *http.Request) {
	msg := "no such endpoint: " + r.URL.Path
	if strings.HasPrefix(r.URL.Path, "/api/0/organizations/") ||
		strings.HasPrefix(r.URL.Path, "/api/0/teams/") {
		sentryErr(w, http.StatusNotFound, msg)
		return
	}
	writeErr(w, http.StatusNotFound, msg)
}

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.code = code
	r.ResponseWriter.WriteHeader(code)
}
