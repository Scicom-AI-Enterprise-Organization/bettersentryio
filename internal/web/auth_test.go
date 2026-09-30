package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The session cookie is Secure and HttpOnly on both issue and clear, whatever the
// request looked like — behind the TLS edge r.TLS is always nil.
func TestSessionCookieIsSecureAndHttpOnly(t *testing.T) {
	a, err := NewAuth("ops", "correct horse", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for name, write := range map[string]func(http.ResponseWriter){
		"set":   a.SetCookie,
		"clear": a.Clear,
	} {
		w := httptest.NewRecorder()
		write(w)
		cookies := w.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatalf("%s: got %d cookies", name, len(cookies))
		}
		c := cookies[0]
		if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
			t.Errorf("%s: Secure=%v HttpOnly=%v SameSite=%v", name, c.Secure, c.HttpOnly, c.SameSite)
		}
	}
}

func TestLocalPathRefusesOffSiteRedirects(t *testing.T) {
	for next, want := range map[string]string{
		"/monitors/x":        "/monitors/x",
		"/incidents?page=2":  "/incidents?page=2",
		"":                   "/",
		"https://evil.test/": "/",
		"//evil.test/":       "/",
		`/\evil.test/`:       "/",
		`/monitors\..\x`:     "/",
	} {
		if got := localPath(next); got != want {
			t.Errorf("localPath(%q) = %q, want %q", next, got, want)
		}
	}
}
