package api

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/metrics"
)

func TestAuditable(t *testing.T) {
	cases := []struct {
		method, path string
		want         bool
	}{
		// Control plane: audited.
		{"POST", "/api/0/apps", true},
		{"DELETE", "/api/0/tokens/3", true},
		{"PUT", "/api/0/apps/tts/retention", true},
		{"POST", "/api/0/issues/9/resolve", true},
		{"POST", "/api/0/channels/test", true},
		// Reads: never.
		{"GET", "/api/0/audit", false},
		{"GET", "/api/0/issues", false},
		// Data plane: excluded by name — at production rates it would make audit_log
		// the biggest table in the database.
		{"POST", "/api/0/beat/tts-batcher", false},
		{"POST", "/api/0/errors", false},
		{"POST", "/api/1/envelope", false},
		// Not the API at all.
		{"POST", "/login", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.path, nil)
		if got := auditable(r); got != c.want {
			t.Errorf("auditable(%s %s) = %v, want %v", c.method, c.path, got, c.want)
		}
	}
}

func TestActorOf(t *testing.T) {
	s := &Server{apiToken: "op-secret"}

	// Operator token + forwarded user: the header is trusted and attributed.
	r := httptest.NewRequest("POST", "/api/0/apps", nil)
	r.Header.Set("Authorization", "Bearer op-secret")
	r.Header.Set("X-BSIO-Actor", "husein@scicom.com.my")
	if actor, via := s.actorOf(r); actor != "husein@scicom.com.my" || via != "session" {
		t.Errorf("forwarded actor = %q via %q", actor, via)
	}

	// Operator token alone.
	r = httptest.NewRequest("POST", "/api/0/apps", nil)
	r.Header.Set("Authorization", "Bearer op-secret")
	if actor, via := s.actorOf(r); actor != "operator" || via != "operator" {
		t.Errorf("bare operator = %q via %q", actor, via)
	}

	// The header WITHOUT the operator token is spoofing, not attribution: anything
	// that can reach the port can send a header, only our server holds the token.
	r = httptest.NewRequest("POST", "/api/0/apps", nil)
	r.Header.Set("Authorization", "Bearer bsiot_deadbeef00")
	r.Header.Set("X-BSIO-Actor", "forged@example.com")
	actor, via := s.actorOf(r)
	if actor == "forged@example.com" {
		t.Fatal("actor header trusted without the operator token")
	}
	if via != "token" || !strings.HasPrefix(actor, "token:bsiot_") {
		t.Errorf("api token = %q via %q", actor, via)
	}

	// An ingest key on the dev-mode fallback is still identified, not anonymous.
	r = httptest.NewRequest("POST", "/api/0/apps", nil)
	r.Header.Set("X-BSIO-Key", "c4cef10f170a4401355f8f41ab7aed8c")
	if actor, via := s.actorOf(r); via != "key" || !strings.HasPrefix(actor, "key:") {
		t.Errorf("ingest key = %q via %q", actor, via)
	}

	r = httptest.NewRequest("POST", "/api/0/apps", nil)
	if actor, via := s.actorOf(r); actor != "anonymous" || via != "none" {
		t.Errorf("no credential = %q via %q", actor, via)
	}
}

func TestCodeClass(t *testing.T) {
	for code, want := range map[int]string{200: "2xx", 302: "3xx", 404: "4xx", 429: "4xx", 500: "5xx"} {
		if got := codeClass(code); got != want {
			t.Errorf("codeClass(%d) = %q, want %q", code, got, want)
		}
	}
}

func TestMetricsHandlerRenders(t *testing.T) {
	c := metrics.NewCounter("bsio_test_total", "test counter")
	c.Add(3)
	v := metrics.NewCounterVec("bsio_test_by_kind_total", "test vec", "kind")
	v.With("a").Inc()
	v.With("b").Add(2)
	metrics.NewGauge("bsio_test_gauge", "test gauge", func() float64 { return 7.5 })

	rec := httptest.NewRecorder()
	metrics.Handler()(rec, httptest.NewRequest("GET", "/-/metrics", nil))
	body := rec.Body.String()

	for _, want := range []string{
		"# TYPE bsio_test_total counter",
		"bsio_test_total 3",
		`bsio_test_by_kind_total{kind="a"} 1`,
		`bsio_test_by_kind_total{kind="b"} 2`,
		"# TYPE bsio_test_gauge gauge",
		"bsio_test_gauge 7.5",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("exposition missing %q\n%s", want, body)
		}
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain; version=0.0.4") {
		t.Errorf("content type = %q", ct)
	}
}
