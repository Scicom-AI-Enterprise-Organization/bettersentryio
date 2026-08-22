package api

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestSentryInterval(t *testing.T) {
	cases := []struct {
		asked string
		span  time.Duration
		want  time.Duration
	}{
		// An explicit interval is honoured verbatim, in each unit Sentry uses.
		{"30s", time.Hour, 30 * time.Second},
		{"5m", 24 * time.Hour, 5 * time.Minute},
		{"2h", 7 * 24 * time.Hour, 2 * time.Hour},
		{"1d", 30 * 24 * time.Hour, 24 * time.Hour},
		// Automatic choices snap up to a width that divides a day, so buckets start
		// on wall-clock boundaries: 24h/100 is 14m24s, which becomes 15m.
		{"", 24 * time.Hour, 15 * time.Minute},
		{"", 30 * 24 * time.Hour, 12 * time.Hour},
		{"", time.Hour, time.Minute},
		// A year would want 3d15h; the ladder stops at a week.
		{"", 365 * 24 * time.Hour, 7 * 24 * time.Hour},
		// Garbage is treated as absent rather than rejected: the panel still draws.
		{"fortnight", 24 * time.Hour, 15 * time.Minute},
	}
	for _, c := range cases {
		if got := sentryInterval(c.asked, c.span); got != c.want {
			t.Errorf("sentryInterval(%q, %v) = %v, want %v", c.asked, c.span, got, c.want)
		}
	}
}

func TestNiceIntervalsDivideADay(t *testing.T) {
	// The property the automatic ladder exists for. Break it and buckets drift off
	// wall-clock boundaries, which is how the axis stops making sense.
	for _, d := range niceIntervals {
		if d <= 24*time.Hour && (24*time.Hour)%d != 0 {
			t.Errorf("%v does not divide a day", d)
		}
	}
}

func TestSentryTime(t *testing.T) {
	fallback := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	want := time.Date(2026, 8, 22, 4, 30, 0, 0, time.UTC)
	// The datasource sends a zoneless stamp for issues and events, RFC3339 for the
	// stats endpoints, and a hand-written variable may hold unix seconds.
	for _, v := range []string{
		"2026-08-22T04:30:00",
		"2026-08-22T04:30:00Z",
		"2026-08-22T12:30:00+08:00",
		"1787373000",
	} {
		if got := sentryTime(v, fallback); !got.Equal(want) {
			t.Errorf("sentryTime(%q) = %v, want %v", v, got, want)
		}
	}
	if got := sentryTime("", fallback); !got.Equal(fallback) {
		t.Errorf("empty should fall back, got %v", got)
	}
	if got := sentryTime("not a time", fallback); !got.Equal(fallback) {
		t.Errorf("garbage should fall back, got %v", got)
	}
}

func TestSentryWindowStatsPeriod(t *testing.T) {
	q := map[string][]string{"statsPeriod": {"7d"}}
	from, to := sentryWindow(q)
	if span := to.Sub(from); span < 7*24*time.Hour-time.Second || span > 7*24*time.Hour+time.Second {
		t.Errorf("statsPeriod=7d gave a %v window", span)
	}
	// An explicit start wins: statsPeriod is the fallback, not an override.
	q = map[string][]string{"statsPeriod": {"7d"}, "start": {"2026-08-20T00:00:00"}, "end": {"2026-08-21T00:00:00"}}
	from, to = sentryWindow(q)
	if span := to.Sub(from); span != 24*time.Hour {
		t.Errorf("explicit start/end gave a %v window", span)
	}
}

func TestSentrySearch(t *testing.T) {
	s := &Server{}
	r := httptest.NewRequest("GET", "/?environment=production&project=3&project=-1", nil)

	got, err := s.sentrySearch(r, `is:unresolved level:error release:tts@1.2 gpu:0 title:"connection refused" boom`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != "unresolved" || got.Level != "error" {
		t.Errorf("status/level = %q/%q", got.Status, got.Level)
	}
	// release: is a column in Sentry and a tag here, because ingest merges every
	// client and server tag into issues.tags. gpu:0 proves the key space is open.
	if got.Tags["release"] != "tts@1.2" || got.Tags["gpu"] != "0" {
		t.Errorf("tags = %v", got.Tags)
	}
	if len(got.Text) != 2 || got.Text[0] != "connection refused" || got.Text[1] != "boom" {
		t.Errorf("text = %q", got.Text)
	}
	if len(got.Environments) != 1 || got.Environments[0] != "production" {
		t.Errorf("environments = %q", got.Environments)
	}
	// -1 is Sentry's "all projects" and must not become a filter on project 0.
	if len(got.ProjectIDs) != 1 || got.ProjectIDs[0] != 3 {
		t.Errorf("projectIDs = %v", got.ProjectIDs)
	}

	// is:ignored is Sentry's wire name for archived.
	if got, err = s.sentrySearch(r, "is:ignored"); err != nil || got.Status != "archived" {
		t.Errorf("is:ignored = %q, %v", got.Status, err)
	}

	// The terms that must fail rather than quietly match everything: guessing here
	// would answer a different question than the one asked.
	for _, q := range []string{"!level:error", "level:error OR level:warning", "(level:error)", "is:assigned"} {
		if _, err := s.sentrySearch(r, q); err == nil {
			t.Errorf("%q should be rejected", q)
		}
	}
}

func TestSentrySearchIssueID(t *testing.T) {
	s := &Server{}
	r := httptest.NewRequest("GET", "/", nil)
	got, err := s.sentrySearch(r, "issue.id:42")
	if err != nil {
		t.Fatal(err)
	}
	// Not a tag lookup: issues.tags has no issue.id, so treating it as one would
	// match nothing and look like "no errors".
	if len(got.IssueIDs) != 1 || got.IssueIDs[0] != 42 {
		t.Errorf("issueIDs = %v", got.IssueIDs)
	}
	if _, ok := got.Tags["issue.id"]; ok {
		t.Error("issue.id leaked into the tag filters")
	}
}

func TestSplitQuoted(t *testing.T) {
	got := splitQuoted(`is:unresolved title:"connection refused" bare`)
	want := []string{"is:unresolved", `title:"connection refused"`, "bare"}
	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for n := range want {
		if got[n] != want[n] {
			t.Errorf("token %d = %q, want %q", n, got[n], want[n])
		}
	}
}

func TestSentryDuration(t *testing.T) {
	cases := map[string]time.Duration{
		"30s": 30 * time.Second,
		"90m": 90 * time.Minute,
		"2h":  2 * time.Hour,
		"14d": 14 * 24 * time.Hour,
		"1w":  7 * 24 * time.Hour,
		"":    0,
		"5":   0,
		"-1d": 0,
		"1y":  0,
	}
	for in, want := range cases {
		if got := sentryDuration(in); got != want {
			t.Errorf("sentryDuration(%q) = %v, want %v", in, got, want)
		}
	}
}
