package api

import (
	"testing"
	"time"
)

// The estimator only has to land in the right order of magnitude — grace
// absorbs the rest — but the common crontab shapes must map to their period,
// not to the conservative fallback.
func TestCronPeriod(t *testing.T) {
	cases := []struct {
		expr string
		want time.Duration
	}{
		{"*/15 * * * *", 15 * time.Minute},
		{"* * * * *", time.Minute},
		{"0 * * * *", time.Hour},
		{"30 */6 * * *", 6 * time.Hour},
		{"0 3 * * *", 24 * time.Hour},
		{"0 3 * * 1", 7 * 24 * time.Hour},
		{"0 3 1 * *", 31 * 24 * time.Hour},
		{"not a crontab", 0},
		{"0 3 * *", 0}, // 4 fields
	}
	for _, c := range cases {
		if got := cronPeriod(c.expr); got != c.want {
			t.Errorf("cronPeriod(%q) = %v, want %v", c.expr, got, c.want)
		}
	}
}

func TestIntervalDuration(t *testing.T) {
	if got := intervalDuration(2, "hour"); got != 2*time.Hour {
		t.Errorf("2 hour = %v", got)
	}
	if got := intervalDuration(1, "fortnight"); got != 0 {
		t.Errorf("unknown unit should be 0, got %v", got)
	}
	if got := intervalDuration(0, "day"); got != 0 {
		t.Errorf("zero interval should be 0, got %v", got)
	}
}
