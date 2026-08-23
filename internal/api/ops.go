// Operability: the audit log, per-project retention, and the metrics endpoint —
// the three things an operator asks for the day this carries someone else's traffic.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/metrics"
	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/store"
)

/* ---- audit ------------------------------------------------------------------ */

// auditQueue decouples audit writes from the requests they describe: recording an
// action must never slow it down or fail it. The cost is honesty about loss — a
// full queue or a crash drops entries, and the dropped counter says so rather than
// pretending the log is gapless.
const auditQueueSize = 256

var auditDropped = metrics.NewCounter("bsio_audit_dropped_total",
	"Audit entries dropped because the write queue was full")

func (s *Server) startAuditWriter() {
	s.audit = make(chan store.AuditEntry, auditQueueSize)
	go func() {
		for e := range s.audit {
			// Its own deadline, not the request's: the request is long gone.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := s.db.InsertAudit(ctx, e); err != nil {
				s.log.Error("audit write failed", "action", e.Action, "err", err)
			}
			cancel()
		}
	}()
}

// auditable reports whether a request is a control-plane mutation. Derived from the
// route, not hand-written per handler, so an endpoint added next month is audited
// without anyone remembering to say so. The data plane — beats, envelopes, error
// ingest — is excluded by name: at production rates it would make audit_log the
// biggest table in the database within a week, and "the service reported an error"
// is not an administrative act.
func auditable(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	p := r.URL.Path
	if !strings.HasPrefix(p, "/api/0/") {
		return false // the envelope endpoint lives at /api/{id}/envelope
	}
	if strings.HasPrefix(p, "/api/0/beat/") || p == "/api/0/errors" {
		return false
	}
	return true
}

// actorOf classifies who is asking and how. The X-BSIO-Actor header carries the
// signed-in user's email from the Next UI — trusted only alongside the operator
// token, because that token is what proves the header came from our server and not
// from anything that can reach the port.
func (s *Server) actorOf(r *http.Request) (actor, via string) {
	if s.hasAPIToken(r) {
		if a := strings.TrimSpace(r.Header.Get("X-BSIO-Actor")); a != "" && len(a) <= 200 {
			return a, "session"
		}
		return "operator", "operator"
	}
	if s.session != nil && s.session.Authenticated(r) {
		return "engine-ui", "session"
	}
	key := presentedKey(r)
	switch {
	case strings.HasPrefix(key, store.TokenPrefix):
		// Tokens cannot administer; an entry with this actor is a denied attempt,
		// which is exactly what an audit log is for.
		return "token:" + truncateKey(key), "token"
	case key != "":
		return "key:" + truncateKey(key), "key"
	}
	return "anonymous", "none"
}

func truncateKey(k string) string {
	if len(k) > 14 {
		return k[:14]
	}
	return k
}

// recordAudit enqueues one entry; it never blocks the request that generated it.
func (s *Server) recordAudit(r *http.Request, status int) {
	actor, via := s.actorOf(r)
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	e := store.AuditEntry{
		Actor: actor, Via: via,
		Action:     r.Method + " " + r.URL.Path,
		Status:     status,
		RemoteAddr: host,
	}
	select {
	case s.audit <- e:
	default:
		auditDropped.Inc()
	}
}

// parseCursor reads a "<timestamp>,<id>" page cursor. Both halves are the row's sort
// key: an id alone would only be a valid position while id order and `at` order agree.
//
// An empty string is no cursor, which is the first page. Anything else that does not
// parse is an error rather than a silent first page — a mangled link should say so, not
// quietly show the top of the log as though that were what was asked for.
func parseCursor(v string) (*store.Cursor, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil
	}
	at, idPart, ok := strings.Cut(v, ",")
	if !ok {
		return nil, fmt.Errorf("cursor %q has no id", v)
	}
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(at))
	if err != nil {
		return nil, fmt.Errorf("cursor timestamp: %w", err)
	}
	id, err := strconv.ParseInt(strings.TrimSpace(idPart), 10, 64)
	if err != nil || id <= 0 {
		return nil, fmt.Errorf("cursor id %q", idPart)
	}
	return &store.Cursor{At: t, ID: id}, nil
}

func (s *Server) handleAuditList(w http.ResponseWriter, r *http.Request) {
	if !s.mayAdminister(r) {
		writeErr(w, http.StatusForbidden, "the audit log requires the operator token or a signed-in session")
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	// One direction at a time. Both set is a malformed link, and silently honouring
	// one of them would page somewhere the caller did not ask for.
	if q.Get("before") != "" && q.Get("after") != "" {
		writeErr(w, http.StatusBadRequest, "pass before or after, not both")
		return
	}
	before, err := parseCursor(q.Get("before"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "before must be <timestamp>,<id>")
		return
	}
	after, err := parseCursor(q.Get("after"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "after must be <timestamp>,<id>")
		return
	}

	query := store.AuditQuery{
		Actor:  q.Get("actor"),
		Action: q.Get("action"),
		Limit:  limit,
		Before: before,
		After:  after,
	}
	// Same window vocabulary as every other stats endpoint — ?statsPeriod=30d, or an
	// explicit ?start=&end= — so a link into the audit log reads like a link into the
	// issue list. Absent all three the log is unbounded, which is what a script asking
	// for "everything since forever" wants; the UI always sends a window.
	if q.Get("statsPeriod") != "" || q.Get("start") != "" || q.Get("end") != "" {
		from, to := sentryWindow(q)
		query.Since = &from
		// Only bound above when an end was actually given: the default window ends
		// "now", and a hard ceiling there would drop a row written while the page
		// was rendering.
		if q.Get("end") != "" {
			query.Until = &to
		}
	}

	page, err := s.db.ListAudit(r.Context(), query)
	if err != nil {
		s.log.Error("audit list failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, page)
}

/* ---- retention -------------------------------------------------------------- */

// handleSetRetention sets how long a project keeps error events. 0 restores the
// default: keep forever. Admin-only for the same reason deleting an app is — an
// ingest key proves a service can report, not that its holder may erase history.
func (s *Server) handleSetRetention(w http.ResponseWriter, r *http.Request) {
	if !s.mayAdminister(r) {
		writeErr(w, http.StatusForbidden, "changing retention requires the operator token or a signed-in session")
		return
	}
	var body struct {
		Days int `json:"days"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "expected a JSON body with days")
		return
	}
	if body.Days < 0 || body.Days > 3650 {
		writeErr(w, http.StatusBadRequest, "days must be between 0 (keep forever) and 3650")
		return
	}
	slug := r.PathValue("slug")
	tag, err := s.db.Exec(r.Context(),
		`update projects set retention_days = $2 where slug = $1`, slug, body.Days)
	if err != nil {
		s.log.Error("set retention failed", "slug", slug, "err", err)
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if tag.RowsAffected() == 0 {
		writeErr(w, http.StatusNotFound, "no such app")
		return
	}
	s.log.Info("retention changed", "app", slug, "days", body.Days)
	writeJSON(w, http.StatusOK, map[string]any{"slug": slug, "retention_days": body.Days})
}

/* ---- metrics ---------------------------------------------------------------- */

var (
	httpRequests = metrics.NewCounterVec("bsio_http_requests_total",
		"HTTP requests by status class", "code")
	ingestEvents = metrics.NewCounter("bsio_ingest_events_total",
		"Error events stored (envelope and native)")
	ingestRejected = metrics.NewCounterVec("bsio_ingest_rejected_total",
		"Ingest requests rejected, by reason", "reason")
	beatsTotal = metrics.NewCounter("bsio_beats_total",
		"Heartbeats accepted")
)

// registerGauges wires the live values: read at scrape time, so they are never a
// stale copy maintained by a background loop.
func (s *Server) registerGauges() {
	metrics.NewGauge("bsio_db_pool_conns", "Open connections in the pgx pool",
		func() float64 { return float64(s.db.Stat().TotalConns()) })
	metrics.NewGauge("bsio_db_pool_acquired", "Connections currently checked out",
		func() float64 { return float64(s.db.Stat().AcquiredConns()) })
	metrics.NewGauge("bsio_detector_tick_age_seconds", "Seconds since the detector last completed a sweep",
		func() float64 { return s.detector.LastTickAge().Seconds() })
	// Alert on `bsio_detector_leader == 1 and bsio_detector_tick_age_seconds > N`,
	// and on `sum(bsio_detector_leader) != 1` across replicas — a standby's stale
	// tick age alone is healthy.
	metrics.NewGauge("bsio_detector_leader", "1 when this replica holds the detector lock and sweeps",
		func() float64 {
			if s.detector.Leading() {
				return 1
			}
			return 0
		})
	metrics.NewGauge("bsio_detector_consecutive_failures", "Consecutive failed detector sweeps",
		func() float64 { return float64(s.detector.Failures()) })
	metrics.NewGauge("bsio_alerts_sent_total", "Alert deliveries confirmed",
		func() float64 { return float64(s.alerter.Sent()) })
	metrics.NewGauge("bsio_alerts_failed_total", "Alert deliveries that exhausted retries",
		func() float64 { return float64(s.alerter.Failed()) })
	metrics.NewGauge("bsio_alerts_dropped_total", "Alerts dropped because the queue was full",
		func() float64 { return float64(s.alerter.Dropped()) })
	metrics.NewGauge("bsio_alert_queue_depth", "Alerts waiting for delivery",
		func() float64 { return float64(s.alerter.QueueDepth()) })
	metrics.NewGauge("bsio_uptime_seconds", "Seconds since the engine started",
		func() float64 { return time.Since(s.started).Seconds() })
}

func codeClass(code int) string {
	switch {
	case code < 300:
		return "2xx"
	case code < 400:
		return "3xx"
	case code < 500:
		return "4xx"
	}
	return "5xx"
}
