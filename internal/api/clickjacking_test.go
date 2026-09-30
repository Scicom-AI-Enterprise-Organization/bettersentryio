package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/alert"
	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/monitor"
	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/store"
	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/web"
)

func assertNotFrameable(t *testing.T, what string, h http.Header) {
	t.Helper()
	if got := h.Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("%s: X-Frame-Options = %q, want DENY (legacy browsers)", what, got)
	}
	if got := h.Get("Content-Security-Policy"); !strings.Contains(got, "frame-ancestors 'none'") {
		t.Errorf("%s: CSP = %q, want frame-ancestors 'none'", what, got)
	}
}

// VAPT SAST #51 (potential clickjacking on legacy browsers, dashboard.html:2): the legacy
// operator UI, served by the real internal/web routes behind the engine's handler, can
// be framed by nobody — modern browsers via CSP, legacy ones via X-Frame-Options.
func TestLegacyUIPagesCannotBeFramed(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	auth, _ := web.NewAuth("ops", "correct horse", time.Hour)

	var engine *monitor.Engine
	dsn := os.Getenv("BSIO_TEST_DATABASE_URL")
	if dsn != "" {
		db, err := store.Open(context.Background(), dsn, 2)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(db.Close)
		if _, err := db.Migrate(context.Background()); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		engine = monitor.NewEngine(db, alert.New(db, log, 1), log, "")
	}
	ui, err := web.New(engine, auth, log)
	if err != nil {
		t.Fatal(err)
	}
	h := (&Server{log: log, apiToken: "operator-token", session: auth}).Handler(ui)

	for _, path := range []string{"/login", "/", "/static/app.css", "/static/app.js"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		assertNotFrameable(t, "GET "+path, w.Result().Header)
	}

	if engine == nil {
		t.Log("BSIO_TEST_DATABASE_URL unset: the signed-in dashboard render is skipped")
		return
	}
	// Signed in, the dashboard itself (dashboard.html) renders and is not frameable.
	form := url.Values{"username": {"ops"}, "password": {"correct horse"}}
	r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("sign-in issued %d cookies", len(cookies))
	}
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(cookies[0])
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Need attention") {
		t.Fatalf("dashboard: status %d, want the rendered dashboard", w.Code)
	}
	assertNotFrameable(t, "GET / (dashboard)", w.Result().Header)
}
