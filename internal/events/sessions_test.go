package events

import (
	"testing"
	"time"
)

var sessNow = time.Date(2026, 8, 19, 10, 30, 0, 0, time.UTC)

// Only endings count. An ongoing "ok" update would double-book the session
// when its exit arrives, so it must parse to nothing.
func TestSessionItemCountsOnlyTerminal(t *testing.T) {
	if c := ParseSessionItem([]byte(`{"status":"ok","attrs":{"release":"v1"}}`), sessNow); c != nil {
		t.Fatalf("ongoing session counted: %+v", c)
	}
	c := ParseSessionItem([]byte(`{"status":"exited","errors":0,"attrs":{"release":"v1","environment":"prod"}}`), sessNow)
	if c == nil || c.Exited != 1 || c.Errored != 0 {
		t.Fatalf("clean exit misread: %+v", c)
	}
	c = ParseSessionItem([]byte(`{"status":"exited","errors":2,"attrs":{}}`), sessNow)
	if c == nil || c.Errored != 1 || c.Exited != 0 {
		t.Fatalf("errored exit misread: %+v", c)
	}
	if c.Environment != "production" {
		t.Fatalf("missing environment should default to production, got %q", c.Environment)
	}
	c = ParseSessionItem([]byte(`{"status":"crashed","attrs":{"release":"v2"}}`), sessNow)
	if c == nil || c.Crashed != 1 {
		t.Fatalf("crash misread: %+v", c)
	}
}

// Request-mode SDKs pre-bucket; each aggregate keeps its own hour.
func TestSessionsAggregates(t *testing.T) {
	payload := []byte(`{"aggregates":[
		{"started":"2026-08-19T09:00:00Z","exited":40,"crashed":2},
		{"started":"2026-08-19T10:00:00Z","exited":10,"errored":1},
		{"started":"2026-08-19T08:00:00Z"}
	],"attrs":{"release":"v3","environment":"tm-h20"}}`)
	got := ParseSessionsItem(payload, sessNow)
	if len(got) != 2 { // the all-zero bucket must not produce a row
		t.Fatalf("want 2 buckets, got %d: %+v", len(got), got)
	}
	if got[0].Exited != 40 || got[0].Crashed != 2 || got[0].Release != "v3" {
		t.Fatalf("first bucket misread: %+v", got[0])
	}
	if !got[0].Hour.Equal(time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("hour not preserved: %v", got[0].Hour)
	}
}

// A skewed clock must not write history — same clamp as event timestamps.
func TestSessionHourClampsSkew(t *testing.T) {
	c := ParseSessionItem([]byte(`{"status":"exited","timestamp":"2031-01-01T00:00:00Z"}`), sessNow)
	if c == nil || !c.Hour.Equal(sessNow.Truncate(time.Hour)) {
		t.Fatalf("future timestamp not clamped: %+v", c)
	}
}
