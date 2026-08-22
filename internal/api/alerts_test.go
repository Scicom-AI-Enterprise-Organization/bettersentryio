package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/alert"
)

// handleTestChannel touches no tables, so a Server with only an alerter and a token
// is enough to drive it.
func testServer() *Server {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &Server{
		alerter:  alert.New(nil, log, 1),
		log:      log,
		apiToken: "operator-token",
	}
}

func postTest(t *testing.T, s *Server, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/0/channels/test", strings.NewReader(body))
	if token != "" {
		r.Header.Set("X-BSIO-Key", token)
	}
	w := httptest.NewRecorder()
	s.handleTestChannel(w, r)
	return w
}

// A transport failure is a 502 carrying the transport's own words, not a 400 and not
// a silent pass. The 200 branch needs real TLS and lives in internal/alert, where the
// test can hand the alerter a client that trusts the test certificate.
func TestHandleTestChannelReportsTransportFailure(t *testing.T) {
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	// An https URL in front of a server that speaks plain http: the handshake fails.
	w := postTest(t, testServer(), "operator-token",
		`{"type":"webhook","url":"`+strings.Replace(upstream.URL, "http://", "https://", 1)+`"}`)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("code = %d, want 502", w.Code)
	}
	if body := w.Body.String(); !strings.Contains(body, "error") || len(body) < 20 {
		t.Errorf("502 should explain itself, got %s", body)
	}
	if hits != 0 {
		t.Fatalf("hits = %d, want 0 — TLS should have failed before any request landed", hits)
	}
}

func TestHandleTestChannelRejectsPlainHTTP(t *testing.T) {
	w := postTest(t, testServer(), "operator-token", `{"type":"teams","url":"http://example.com/x"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "https://") {
		t.Errorf("body does not say what is wrong: %s", w.Body.String())
	}
}

func TestHandleTestChannelRejectsUnknownType(t *testing.T) {
	w := postTest(t, testServer(), "operator-token", `{"type":"pigeon","url":"https://example.com/x"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", w.Code)
	}
}

// Testing a webhook reaches out to the network on the caller's behalf, so it is gated
// like the writes, not like the reads.
func TestHandleTestChannelNeedsTheOperatorToken(t *testing.T) {
	w := postTest(t, testServer(), "", `{"type":"teams","url":"https://example.com/x"}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", w.Code)
	}
}

// A 2 KB HTML error page must not land whole in a toast.
func TestTrimErrorCaps(t *testing.T) {
	long := strings.Repeat("x", 900)
	got := trimError(&stringError{long})
	if len(got) > 320 {
		t.Fatalf("len = %d, want it capped near 300", len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("a truncated message should say so: %q", got[len(got)-8:])
	}
}

// Short messages pass through untouched: the upstream's own words are the point.
func TestTrimErrorKeepsShortMessages(t *testing.T) {
	if got := trimError(&stringError{"teams returned 404: no such flow"}); got != "teams returned 404: no such flow" {
		t.Fatalf("got %q", got)
	}
}

type stringError struct{ s string }

func (e *stringError) Error() string { return e.s }

// The patience menu and its validator must not drift apart: the handler rejects
// anything not on the menu, so a value the UI offers has to be on it.
func TestPatienceChoicesAreSaneAndSorted(t *testing.T) {
	if patienceChoices[0] != 0 {
		t.Errorf("the menu must offer 0 (off), got %d", patienceChoices[0])
	}
	for i := 1; i < len(patienceChoices); i++ {
		if patienceChoices[i] <= patienceChoices[i-1] {
			t.Fatalf("choices not ascending at %d: %v", i, patienceChoices)
		}
		if patienceChoices[i] > 86400 {
			t.Fatalf("choice %d exceeds the DB check constraint of 86400", patienceChoices[i])
		}
	}
	// The default a fresh project gets, per migration 0007, has to be offerable.
	var found bool
	for _, c := range patienceChoices {
		if c == 600 {
			found = true
		}
	}
	if !found {
		t.Error("600s is the column default; the menu must be able to show it")
	}
}

// The DTO is the contract the web client reads; a renamed field breaks the page
// silently.
func TestChannelDTOFieldNames(t *testing.T) {
	raw, err := json.Marshal(channelDTO{ID: 1, Name: "n", Type: "teams", URLMasked: "u", Enabled: true, Imported: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"id"`, `"name"`, `"type"`, `"url_masked"`, `"enabled"`, `"imported"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("%s missing from %s", want, raw)
		}
	}
}
