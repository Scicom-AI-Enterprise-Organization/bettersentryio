package api

import (
	"testing"
	"time"
)

// Discover hands its rows back as map[string]any straight out of pgx, so the coercions
// in analytics.go are the only logic of the handler's own. What they must never do is
// panic or invent a value: an aggregate that came back in an unexpected shape is a zero
// and a missing string is empty, because a tile reading 0 is honest and a crashed page
// is not.
func TestRowCoercions(t *testing.T) {
	// count() is int64 and count_unique() can be either width depending on the plan.
	for _, c := range []struct {
		in   any
		want int64
	}{
		{int64(7), 7},
		{int32(7), 7},
		{float64(7.9), 7}, // truncates, as a count of events must
		{nil, 0},
		{"7", 0}, // a text column where a number was expected is not silently parsed
	} {
		if got := rowInt(c.in); got != c.want {
			t.Errorf("rowInt(%#v) = %d, want %d", c.in, got, c.want)
		}
	}

	if got := rowString("vllm/worker.py in execute_model"); got != "vllm/worker.py in execute_model" {
		t.Errorf("rowString = %q", got)
	}
	// A null culprit is common — issues grouped from a bare message have none.
	if got := rowString(nil); got != "" {
		t.Errorf("rowString(nil) = %q, want empty", got)
	}

	at := time.Date(2026, 8, 22, 4, 30, 0, 0, time.UTC)
	if got := rowTime(at); !got.Equal(at) {
		t.Errorf("rowTime = %v, want %v", got, at)
	}
	if got := rowTime(nil); !got.IsZero() {
		t.Errorf("rowTime(nil) = %v, want the zero time", got)
	}
}
