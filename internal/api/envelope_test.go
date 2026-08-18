package api

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/events"
)

func TestSentryKeyFromHeader(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/2/envelope/", nil)
	r.Header.Set("X-Sentry-Auth",
		"Sentry sentry_key=33b0776db026f13aaf5387fe58264dcc, sentry_version=7, sentry_client=sentry.python/2.68.0")
	if got := sentryKey(r); got != "33b0776db026f13aaf5387fe58264dcc" {
		t.Fatalf("header key = %q", got)
	}
}

func TestSentryKeyFromQuery(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/2/envelope/?sentry_key=abc123", nil)
	if got := sentryKey(r); got != "abc123" {
		t.Fatalf("query key = %q", got)
	}
}

// Length-prefixed and newline-delimited items in one envelope, exactly the two
// framings the spec allows.
func TestParseEnvelopeFramings(t *testing.T) {
	env := `{"event_id":"aa"}` + "\n" +
		`{"type":"event","length":13}` + "\n" +
		`{"message":1}` + "\n" +
		`{"type":"client_report"}` + "\n" +
		`{"discarded":[]}` + "\n"
	header, items, err := parseEnvelope([]byte(env))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(header, []byte("aa")) {
		t.Fatalf("header = %s", header)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if items[0].Type != "event" || string(items[0].Payload) != `{"message":1}` {
		t.Fatalf("item 0 = %+v", items[0])
	}
	if items[1].Type != "client_report" || string(items[1].Payload) != `{"discarded":[]}` {
		t.Fatalf("item 1 = %+v", items[1])
	}
}

func TestParseEnvelopeRejectsLyingLength(t *testing.T) {
	env := "{}\n" + `{"type":"event","length":9999}` + "\nshort"
	if _, _, err := parseEnvelope([]byte(env)); err == nil {
		t.Fatal("length past the end of the envelope must be an error")
	}
}

// The golden fixtures are real sentry-sdk 2.68 output, byte-for-byte. If these
// stop parsing, we broke drop-in compatibility, whatever the unit tests say.
func TestGoldenEnvelopes(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "envelopes")
	cases := []struct {
		file      string
		wantExc   bool
		wantInMsg string
	}{
		{"python-keyerror.envelope", true, ""},
		{"python-message.envelope", false, "Error loading audio file"},
		{"python-logging-exc.envelope", true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, tc.file))
			if err != nil {
				t.Skipf("fixture missing: %v", err)
			}
			_, items, err := parseEnvelope(raw)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			var evItem []byte
			for _, it := range items {
				if it.Type == "event" {
					evItem = it.Payload
					break
				}
			}
			if evItem == nil {
				t.Fatalf("no event item among %d items", len(items))
			}
			var e events.Event
			if err := json.Unmarshal(evItem, &e); err != nil {
				t.Fatalf("unmarshal real SDK event: %v", err)
			}
			if tc.wantExc && (e.Exception == nil || len(e.Exception.Values) == 0) {
				t.Fatal("expected an exception")
			}
			if e.Message == "" && e.Logentry != nil {
				e.Message = e.Logentry.Formatted
			}
			if tc.wantInMsg != "" && !strings.Contains(e.Message, tc.wantInMsg) {
				t.Fatalf("message = %q, want substring %q", e.Message, tc.wantInMsg)
			}
			if e.Timestamp == nil || e.Timestamp.IsZero() {
				t.Fatal("real SDK timestamp did not decode")
			}
			if len(e.Tags) == 0 {
				t.Fatal("real SDK tags did not decode")
			}
			fp, kind, culprit, _ := events.Fingerprint(&e)
			if fp == "" || kind == "" {
				t.Fatalf("fingerprint incomplete: %q %q", fp, kind)
			}
			// A bare capture_message has no transaction and no logger, so an
			// empty culprit is correct there; exceptions must always have one.
			if tc.wantExc && culprit == "" {
				t.Fatal("exception event lost its culprit")
			}
			// The SDK's own bytes must round-trip through jsonb intact — locals,
			// breadcrumbs and source context all live outside our struct.
			if tc.wantExc && !bytes.Contains(evItem, []byte(`"vars"`)) {
				t.Fatal("fixture lost frame locals — regenerate it")
			}
		})
	}
}

func TestReadEnvelopeBodyGzip(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "envelopes", "python-keyerror.envelope.gz"))
	if err != nil {
		t.Skipf("fixture missing: %v", err)
	}
	r := httptest.NewRequest("POST", "/api/2/envelope/", bytes.NewReader(raw))
	r.Header.Set("Content-Encoding", "gzip")
	w := httptest.NewRecorder()
	body, err := readEnvelopeBody(w, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, items, err := parseEnvelope(body); err != nil || len(items) == 0 {
		t.Fatalf("gzip round-trip: err=%v items=%d", err, len(items))
	}
}

func TestReadEnvelopeBodyRejectsUnknownEncoding(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write([]byte("{}\n"))
	_ = zw.Close()
	r := httptest.NewRequest("POST", "/api/2/envelope/", &buf)
	r.Header.Set("Content-Encoding", "br")
	if _, err := readEnvelopeBody(httptest.NewRecorder(), r); err == nil {
		t.Fatal("brotli must be refused, not silently misparsed")
	}
}
