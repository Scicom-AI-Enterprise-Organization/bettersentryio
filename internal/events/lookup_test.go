package events

import (
	"strings"
	"testing"
	"time"
)

func TestLookupWhereWithoutAWindowAddsNoTimeBounds(t *testing.T) {
	// The bug this guards: the window clause was unconditional, so a zero To
	// (`received_at < 0001-01-01`) excluded every event -- and the windowed variant
	// turned Grafana's correlation click into "no data" for any trace older than the
	// pane's time range. Identity is exact; a window on it only manufactures misses.
	args := []any{}
	where, err := lookupWhere(LookupQuery{TraceID: "abc"}, &args)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(where, "received_at") {
		t.Errorf("windowless lookup still bounds time: %q", where)
	}
	if !strings.Contains(where, "trace_id") {
		t.Errorf("trace predicate missing: %q", where)
	}
}

func TestLookupWhereHonoursAnExplicitWindow(t *testing.T) {
	args := []any{}
	q := LookupQuery{
		TraceID: "abc",
		From:    time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC),
		To:      time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC),
	}
	where, err := lookupWhere(q, &args)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(where, "received_at") != 2 {
		t.Errorf("expected both bounds: %q", where)
	}
	// From, To ride along as bind parameters, never interpolated.
	if len(args) != 3 {
		t.Errorf("args = %v", args)
	}
}

func TestLookupWhereComposesAllPredicates(t *testing.T) {
	args := []any{}
	q := LookupQuery{
		Tags:      map[string]string{"correlation_id": "cid-1"},
		TraceID:   "abc",
		ProjectID: 7,
	}
	where, err := lookupWhere(q, &args)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"@>", "trace_id", "project_id"} {
		if !strings.Contains(where, want) {
			t.Errorf("missing %q in %q", want, where)
		}
	}
}
