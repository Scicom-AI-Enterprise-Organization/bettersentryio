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
	apiToken string
	engine   *monitor.Engine
	detector *monitor.Detector
	alerter  *alert.Alerter
	log      *slog.Logger
	version  string
	started  time.Time
	session  SessionChecker
}

// New builds the HTTP surface. apiToken is the operator credential the UI presents;
// when empty the admin endpoints fall back to accepting any ingest key, which is a
// development convenience the caller is expected to warn about loudly.
func New(db *store.DB, e *monitor.Engine, d *monitor.Detector, a *alert.Alerter, log *slog.Logger, version, apiToken string, session SessionChecker) *Server {
	return &Server{
		db: db, engine: e, detector: d, alerter: a, log: log,
		version: version, apiToken: apiToken, started: time.Now(), session: session,
	}
}

func presentedKey(r *http.Request) string {
	if k := r.Header.Get("X-BSIO-Key"); k != "" {
		return k
	}
	return r.URL.Query().Get("key")
}

// hasAPIToken reports whether the caller presented the operator token. Compared in
// constant time so a wrong guess leaks nothing through timing.
func (s *Server) hasAPIToken(r *http.Request) bool {
	if s.apiToken == "" {
		return false
	}
	got := presentedKey(r)
	if got == "" {
		got = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
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
	// The SDK sources, so a service can curl the client off the engine it reports to.
	clients.Routes(mux)
	// Health and readiness stay unauthenticated: a probe should not need a session,
	// and neither reveals anything beyond our own liveness.
	mux.HandleFunc("GET /-/health", s.handleHealth)
	mux.HandleFunc("GET /-/ready", s.handleReady)
	if ui != nil {
		ui.Routes(mux)
	}
	return logging(s.log, mux)
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
	if s.detector.Ticks() == 0 {
		if time.Since(s.started) > maxAge {
			problems = append(problems, "detector has never completed a tick")
		}
	} else if tickAge > maxAge {
		problems = append(problems, "detector tick is stale ("+tickAge.Round(time.Second).String()+")")
	}
	// A detector that runs but fails is reported immediately, not after the
	// staleness timeout — otherwise this endpoint is green over a blind detector.
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

func logging(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rec, r)
		if rec.code >= 400 {
			log.Warn("http", "method", r.Method, "path", r.URL.Path,
				"status", rec.code, "dur_ms", time.Since(start).Milliseconds())
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.code = code
	r.ResponseWriter.WriteHeader(code)
}
