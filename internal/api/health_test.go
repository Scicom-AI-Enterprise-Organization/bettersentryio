package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/alert"
	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/events"
	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/monitor"
	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/store"
)

// A server whose database is down: the pool is lazy, and nothing listens on port 1.
func dbDownServer(t *testing.T) *Server {
	t.Helper()
	db, err := store.Open(context.Background(),
		"postgres://bsio_probe_user@127.0.0.1:1/bsio_probe_db?sslmode=disable&connect_timeout=1", 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := alert.New(nil, log, 1)
	return &Server{
		db: db, events: events.New(db), alerter: a, log: log, apiToken: "operator-token",
		detector: monitor.NewDetector(db, a, log, time.Second, ""),
		started:  time.Now(),
	}
}

// Internals a pgx dial error carries: the database host, port, user and name.
var internals = []string{"127.0.0.1", "bsio_probe_user", "bsio_probe_db", "dial", "connect"}

// VAPT SAST #45 (improper error handling): the unauthenticated /-/health answers the
// same status and problem list, but the raw error text goes only to the operator.
func TestHealthHidesDatabaseErrorsFromAnonymousCallers(t *testing.T) {
	s := dbDownServer(t)

	w := httptest.NewRecorder()
	s.handleHealth(w, httptest.NewRequest(http.MethodGet, "/-/health", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("anonymous: status %d, want 503", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"database unreachable"`) {
		t.Errorf("anonymous: problem not reported: %s", body)
	}
	for _, leak := range internals {
		if strings.Contains(body, leak) {
			t.Errorf("anonymous: response leaks %q: %s", leak, body)
		}
	}

	r := httptest.NewRequest(http.MethodGet, "/-/health", nil)
	r.Header.Set("Authorization", "Bearer operator-token")
	w = httptest.NewRecorder()
	s.handleHealth(w, r)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "database unreachable: ") {
		t.Errorf("operator: want the detailed problem, got %d %s", w.Code, w.Body.String())
	}
}

// A failed read on the Sentry Web API says so without quoting the database error.
func TestSentryIssuesFailureDoesNotEchoTheDatabaseError(t *testing.T) {
	h := dbDownServer(t).Handler(nil)
	r := httptest.NewRequest(http.MethodGet, "/api/0/organizations/x/issues/", nil)
	r.Header.Set("Authorization", "Bearer operator-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "could not read issues") {
		t.Fatalf("got %d %s, want 503 could not read issues", w.Code, w.Body.String())
	}
	for _, leak := range internals {
		if strings.Contains(w.Body.String(), leak) {
			t.Errorf("response leaks %q: %s", leak, w.Body.String())
		}
	}
}
