package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Every engine response carries the hardening headers — including the SDK download
// (Checkmarx flagged clients.go) and error paths that never reach a handler of ours.
func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	h := notFoundServer().Handler(uiStub{})
	for _, path := range []string{
		"/clients/python/bettersentryio.py", // 200, the public SDK
		"/api/0/no-such-thing",              // 404 from the catch-all
		"/",                                 // 401 from the UI's session gate
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		hdr := w.Result().Header
		if got := hdr.Get("Strict-Transport-Security"); !strings.HasPrefix(got, "max-age=") {
			t.Errorf("%s: Strict-Transport-Security = %q", path, got)
		}
		if got := hdr.Get("X-Frame-Options"); got != "DENY" {
			t.Errorf("%s: X-Frame-Options = %q", path, got)
		}
		if got := hdr.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q", path, got)
		}
		if got := hdr.Get("Content-Security-Policy"); !strings.Contains(got, "frame-ancestors 'none'") {
			t.Errorf("%s: Content-Security-Policy = %q", path, got)
		}
	}
}
