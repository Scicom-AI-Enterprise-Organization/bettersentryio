package events

import (
	"strings"
	"testing"
	"time"
)

func TestAlignDownMatchesDateBin(t *testing.T) {
	// The bug this guards: time.Truncate counts from year 1, date_bin counts from
	// its origin. For any interval that does not divide a day the two disagree, so
	// every bucket Postgres returned fell off the Go axis and the chart read zero.
	origin := time.Unix(0, 0).UTC()
	at := time.Date(2026, 8, 22, 13, 47, 31, 0, time.UTC)
	for _, d := range []time.Duration{
		time.Minute, 15 * time.Minute, time.Hour, 7*time.Hour + 12*time.Minute,
		12 * time.Hour, 24 * time.Hour,
	} {
		got := alignDown(at, d)
		if got.After(at) || at.Sub(got) >= d {
			t.Errorf("alignDown(%v, %v) = %v is not the containing bucket", at, d, got)
		}
		// The boundary must be an exact number of intervals from the origin, which
		// is precisely what date_bin(d, ts, origin) returns.
		if got.Sub(origin)%d != 0 {
			t.Errorf("alignDown(%v, %v) = %v is not an epoch multiple", at, d, got)
		}
	}
}

func TestAlignDownBeforeEpoch(t *testing.T) {
	at := time.Date(1969, 12, 31, 23, 30, 0, 0, time.UTC)
	got := alignDown(at, time.Hour)
	want := time.Date(1969, 12, 31, 23, 0, 0, 0, time.UTC)
	// Floor, not truncate-toward-zero: the latter would round *up* before the epoch
	// and put the point in a bucket that has not started yet.
	if !got.Equal(want) {
		t.Errorf("alignDown = %v, want %v", got, want)
	}
}

func TestFieldSQL(t *testing.T) {
	args := []any{}
	if _, err := fieldSQL("timestamp", &args); err != nil {
		t.Errorf("timestamp should resolve: %v", err)
	}
	// tags[…] is dynamic because ingest keeps every client tag, and it must bind
	// rather than interpolate: the key comes from a Grafana panel.
	expr, err := fieldSQL("tags[gpu]", &args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(expr, "i.tags->>$") {
		t.Errorf("tag field did not bind: %q", expr)
	}
	if len(args) != 1 || args[0] != "gpu" {
		t.Errorf("args = %v", args)
	}
	// An unknown field is an error naming the field. Silently returning an empty
	// column would look like "no data" instead of "you typed it wrong".
	if _, err := fieldSQL("span.duration", &args); err == nil {
		t.Error("unknown field should be rejected")
	} else if !strings.Contains(err.Error(), "span.duration") {
		t.Errorf("error should name the field, got %v", err)
	}
}

func TestAggregateSQL(t *testing.T) {
	args := []any{}
	if expr, isAgg, err := aggregateSQL("count()", &args); err != nil || !isAgg || expr != "count(*)" {
		t.Errorf("count() = %q, %v, %v", expr, isAgg, err)
	}
	if expr, _, err := aggregateSQL("count_unique(issue)", &args); err != nil ||
		!strings.HasPrefix(expr, "count(distinct ") {
		t.Errorf("count_unique(issue) = %q, %v", expr, err)
	}
	// A plain field is not an error, it is simply not an aggregate — callers use the
	// flag to decide whether the query needs a group by.
	if _, isAgg, err := aggregateSQL("level", &args); isAgg || err != nil {
		t.Errorf("level should be reported as a non-aggregate, got %v, %v", isAgg, err)
	}
	if _, _, err := aggregateSQL("count(1)", &args); err == nil {
		t.Error("count() takes no argument")
	}
	if _, _, err := aggregateSQL("p95(duration)", &args); err == nil {
		t.Error("unsupported aggregate should be rejected")
	}
}

func TestSearchClausesBindEverything(t *testing.T) {
	args := []any{}
	f := Search{
		ProjectIDs:   []int64{1, 2},
		Environments: []string{"production"},
		Status:       "unresolved",
		Level:        "error",
		Kind:         "RuntimeError",
		IssueIDs:     []int64{7},
		Tags:         map[string]string{"release": "tts@1", "gpu": "0"},
		Text:         []string{"boom"},
	}
	sql := f.clauses(&args)
	// Ten values: projects, environments, level, kind, issue ids, two tag *pairs*
	// (key and value bind separately), and the title pattern. None of them may
	// appear inline in the SQL.
	if len(args) != 10 {
		t.Errorf("bound %d args: %v", len(args), args)
	}
	for _, leaked := range []string{"production", "RuntimeError", "tts@1", "boom"} {
		if strings.Contains(sql, leaked) {
			t.Errorf("%q was interpolated into the SQL: %s", leaked, sql)
		}
	}
	// Tag order is sorted, not map order, so the same filter always produces the
	// same statement for Postgres to plan once.
	if got := args[5]; got != "gpu" {
		t.Errorf("tag keys should be sorted, first was %v", got)
	}
	if !strings.Contains(sql, "resolved_at is null") {
		t.Errorf("is:unresolved lost its clause: %s", sql)
	}
}

func TestSearchClausesEmpty(t *testing.T) {
	args := []any{}
	if got := (Search{}).clauses(&args); got != "" || len(args) != 0 {
		t.Errorf("an empty search must add nothing, got %q with %v", got, args)
	}
}
