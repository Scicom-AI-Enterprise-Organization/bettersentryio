package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// VAPT SAST #40 (DoS resource exhaustion): every phase of a connection is bounded.
func TestServerBoundsEveryConnectionPhase(t *testing.T) {
	srv := newHTTPServer(":0", http.NotFoundHandler())
	for name, d := range map[string]time.Duration{
		"ReadHeaderTimeout": srv.ReadHeaderTimeout,
		"ReadTimeout":       srv.ReadTimeout,
		"WriteTimeout":      srv.WriteTimeout,
		"IdleTimeout":       srv.IdleTimeout,
	} {
		if d <= 0 || d > 2*time.Minute {
			t.Errorf("%s = %v, want a bound in (0, 2m]", name, d)
		}
	}
	if srv.MaxHeaderBytes <= 0 || srv.MaxHeaderBytes > 64<<10 {
		t.Errorf("MaxHeaderBytes = %d, want <= 64 KiB", srv.MaxHeaderBytes)
	}
}

// The header cap is enforced on the wire: a request whose headers exceed it is
// refused with 431 before any handler runs.
func TestOversizedHeadersAreRefused(t *testing.T) {
	var reached bool
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true })
	ts := httptest.NewUnstartedServer(h)
	ts.Config = newHTTPServer("", h)
	ts.Start()
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/0/issues", nil)
	req.Header.Set("X-Pad", strings.Repeat("a", 200<<10))
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestHeaderFieldsTooLarge {
		t.Fatalf("status = %d, want 431", resp.StatusCode)
	}
	if reached {
		t.Fatal("the handler ran for an oversized request")
	}
}
