package alert

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestChannel touches no tables, so a nil DB is enough to exercise delivery.
func testAlerter() *Alerter { return New(nil, quiet(), 1) }

func TestTestChannelDeliversAProbe(t *testing.T) {
	var (
		hits atomic.Int64
		body []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := testAlerter()
	a.SetBaseURL("http://bsio.example")
	if err := a.TestChannel(context.Background(), "webhook", srv.URL); err != nil {
		t.Fatalf("TestChannel: %v", err)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("hits = %d, want 1", got)
	}

	var ev Event
	if err := json.Unmarshal(body, &ev); err != nil {
		t.Fatalf("decode probe: %v", err)
	}
	if ev.Kind != "channel.test" {
		t.Errorf("kind = %q, want channel.test", ev.Kind)
	}
	// Severity OK keeps a green card out of the on-call escalation path.
	if ev.Severity != SeverityOK {
		t.Errorf("severity = %q, want %q", ev.Severity, SeverityOK)
	}
	if !strings.Contains(ev.Text, "webhook works") {
		t.Errorf("probe text does not say it is a test: %q", ev.Text)
	}
}

// The upstream's own words are the useful half of a failed test: "404" and
// "connection refused" send an operator to different places.
func TestTestChannelReportsWhatTheUpstreamSaid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("no such flow"))
	}))
	defer srv.Close()

	err := testAlerter().TestChannel(context.Background(), "teams", srv.URL)
	if err == nil {
		t.Fatal("want an error for a 404 upstream")
	}
	for _, want := range []string{"404", "no such flow"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// A test is pressed by a human who is watching. One attempt, unlike the live path's
// three — if this starts retrying, the button gets slow enough to distrust.
func TestTestChannelDoesNotRetry(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	a := testAlerter()
	a.SetRetryBackoff(time.Millisecond) // so a regression here fails fast rather than hanging
	if err := a.TestChannel(context.Background(), "webhook", srv.URL); err == nil {
		t.Fatal("want an error for a 500 upstream")
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("hits = %d, want 1 — the test path must not retry", got)
	}
}

// An unknown type must fail before any request goes out.
func TestTestChannelRejectsUnknownType(t *testing.T) {
	err := testAlerter().TestChannel(context.Background(), "carrier-pigeon", "https://example.invalid/x")
	if err == nil || !strings.Contains(err.Error(), "unknown channel type") {
		t.Fatalf("err = %v, want an unknown-channel-type error", err)
	}
}

// The genuine success path, over TLS, since that is the only scheme the API accepts:
// a 200 from an https endpoint is what opens the Add button in the UI.
func TestTestChannelSucceedsOverTLS(t *testing.T) {
	var (
		hits  atomic.Int64
		probe []byte
	)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		probe, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusAccepted) // any 2xx counts
	}))
	defer srv.Close()

	a := testAlerter()
	// srv.Client() trusts the server's generated certificate; nothing else does.
	a.http = srv.Client()

	if !strings.HasPrefix(srv.URL, "https://") {
		t.Fatalf("expected an https test server, got %s", srv.URL)
	}
	if err := a.TestChannel(context.Background(), "teams", srv.URL); err != nil {
		t.Fatalf("TestChannel over TLS: %v", err)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("hits = %d, want 1", got)
	}
	// Teams gets an Adaptive Card in the Workflows envelope, not the raw event.
	if !strings.Contains(string(probe), "AdaptiveCard") {
		t.Errorf("teams probe is not an adaptive card: %s", probe)
	}
}
