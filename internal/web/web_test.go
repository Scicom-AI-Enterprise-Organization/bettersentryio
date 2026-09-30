package web

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func testUI(t *testing.T) http.Handler {
	t.Helper()
	a, err := NewAuth("ops", "correct horse", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(nil, a, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.Routes(mux)
	return mux
}

// VAPT SAST #43 (missing HttpOnly) and #44 (missing Secure), auth.go:75/89, through the
// real routes. The request is plain http with r.TLS == nil, exactly what the engine sees
// behind the TLS edge: the old `Secure: r.TLS != nil` never set the flag there.
func TestLoginAndLogoutRoutesSetSecureHttpOnlyCookies(t *testing.T) {
	ui := testUI(t)

	form := url.Values{"username": {"ops"}, "password": {"correct horse"}, "next": {"/"}}
	r := httptest.NewRequest(http.MethodPost, "http://engine:9090/login", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	ui.ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("login: status %d, want 303", w.Code)
	}
	assertSessionCookieFlags(t, "login", w.Result().Cookies())

	w = httptest.NewRecorder()
	ui.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "http://engine:9090/logout", nil))
	cookies := w.Result().Cookies()
	assertSessionCookieFlags(t, "logout", cookies)
	if cookies[0].MaxAge >= 0 {
		t.Errorf("logout: MaxAge = %d, want the cookie expired", cookies[0].MaxAge)
	}
}

func assertSessionCookieFlags(t *testing.T, step string, cookies []*http.Cookie) {
	t.Helper()
	if len(cookies) != 1 || cookies[0].Name != sessionCookie {
		t.Fatalf("%s: cookies = %v, want one %s", step, cookies, sessionCookie)
	}
	c := cookies[0]
	if !c.HttpOnly {
		t.Errorf("%s: session cookie is not HttpOnly (SAST #43)", step)
	}
	if !c.Secure {
		t.Errorf("%s: session cookie is not Secure (SAST #44)", step)
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("%s: SameSite = %v, want Lax", step, c.SameSite)
	}
}

// A wrong password gets no session at all.
func TestLoginRouteWithWrongPasswordIssuesNoSession(t *testing.T) {
	ui := testUI(t)
	form := url.Values{"username": {"ops"}, "password": {"guess"}}
	r := httptest.NewRequest(http.MethodPost, "http://engine:9090/login", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	ui.ServeHTTP(w, r)
	if len(w.Result().Cookies()) != 0 {
		t.Fatalf("wrong password was issued %v", w.Result().Cookies())
	}
}
