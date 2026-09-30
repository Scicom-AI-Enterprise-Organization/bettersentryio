package events

import (
	"regexp"
	"strings"
	"testing"
)

// VAPT SAST #39 (SQL injection). Every value a dashboard or search box controls is bound
// as a parameter; the SQL text only ever carries placeholders and fixed fragments.

var hostile = []string{
	`x' or '1'='1`,
	`x'); drop table issues; --`,
	`" or 1=1 --`,
	`\'; select pg_sleep(10); --`,
}

func TestSearchClausesBindEveryValue(t *testing.T) {
	for _, h := range hostile {
		f := Search{
			Environments: []string{h},
			Level:        h,
			Kind:         h,
			Tags:         map[string]string{h: h},
			Text:         []string{h},
		}
		var args []any
		sql := f.clauses(&args)
		for _, frag := range []string{"drop", "pg_sleep", "or '1'", "or 1=1", "--"} {
			if strings.Contains(strings.ToLower(sql), frag) {
				t.Fatalf("input %q leaked into the SQL text: %s", h, sql)
			}
		}
		if strings.ContainsAny(sql, `'"\;`) {
			t.Fatalf("SQL text carries a quote or statement separator for input %q: %s", h, sql)
		}
		if len(args) == 0 {
			t.Fatalf("no bind arguments for input %q", h)
		}
	}
}

// Sort is user input too (?sort= from Grafana). It only ever selects a result
// column by position or falls back to a fixed column.
func TestEventOrderByOnlyEmitsPositionsOrAFixedColumn(t *testing.T) {
	positions := map[string]int{"timestamp": 1, "count()": 2}
	ok := regexp.MustCompile(`^([0-9]+|e\.received_at) (asc|desc)$`)
	for _, sort := range append(hostile, "timestamp", "-count()", "", "1; drop table events") {
		got := EventSearch{Sort: sort}.orderBy(positions, 0)
		if !ok.MatchString(got) {
			t.Errorf("orderBy(%q) = %q, want a position or e.received_at", sort, got)
		}
	}
}
