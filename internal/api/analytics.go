// Project analytics: the aggregate numbers behind the volume chart — how much, how it
// compares with the window before it, which dimension the noise is concentrated in, and
// which handful of issues is producing it.
//
// Deliberately *not* a second time series. The chart on the analytics page is drawn from
// /api/0/apps/{slug}/series (internal/api/errors.go), and one endpoint owning the buckets
// is the only way the chart and the figures beside it can agree. What this adds is what a
// series cannot answer: a comparison, a set of breakdowns, and a leaderboard.
//
// Every query goes through the same Discover machinery the Grafana datasource uses
// (internal/events/discover.go), so there is no second aggregation path to drift from it.
package api

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/events"
)

// topIssueRows caps the leaderboard. It answers "where is the noise coming from", not
// "show me every issue" — the issue list is that, and it filters and sorts properly.
const topIssueRows = 10

// breakdownRows caps each dimension. Six is what fits beside two others without the row
// of panels turning into a page of its own; a dimension with more says so (Truncated).
const breakdownRows = 6

// The cross-tab's shape. Wider than this stops being a table somebody reads at a glance,
// and the row cap is what keeps a project with a hundred environments from turning one
// panel into the whole page.
const (
	matrixRows    = 8
	matrixColumns = 6
	matrixCells   = matrixRows * matrixColumns * 2
)

// maxLevelRows caps the level split. Sentry defines five levels; a much larger number
// means an SDK is sending something we have never seen, and a bounded row set is better
// than letting one project's typo'd level widen every response.
const maxLevelRows = 20

// analyticsDimensions are the breakdowns worth a panel, in the order they are shown.
//
// All five are Discover fields, so an unsupported one fails loudly here rather than
// returning an empty column: error.type is the exception class, and release, transaction
// and server_name are client tags every SDK sends when it is configured to.
var analyticsDimensions = []string{
	"error.type", "environment", "release", "transaction", "server_name",
}

// tagDimensionCount is how many of the project's own tags get a panel. Client tags are
// where the domain lives — a GPU index, a handled flag, a queue name — and which ones
// exist is not something we can know in advance, so the panels are discovered rather
// than listed. Three keeps the row of panels finite.
const tagDimensionCount = 3

// maxTagCardinality skips tags that are identifiers rather than dimensions. Six values
// out of nine hundred correlation ids is not a breakdown, it is a sample.
const maxTagCardinality = 50

// fixedTagKeys are already answered by a column or by analyticsDimensions above, so a
// discovered tag panel for them would be the same panel twice.
var fixedTagKeys = map[string]bool{
	"level": true, "environment": true, "release": true,
	"transaction": true, "server_name": true, "error.type": true,
}

type analyticsLevel struct {
	Level string `json:"level"`
	Count int64  `json:"count"`
	// Issues is how many distinct issues carry this level. Level is a property of the
	// issue, so these partition the project rather than double-counting it — which is
	// why summing them gives the window total.
	Issues int64 `json:"issues"`
}

type analyticsRow struct {
	Value  string `json:"value"`
	Count  int64  `json:"count"`
	Issues int64  `json:"issues"`
}

type analyticsBreakdown struct {
	Field string         `json:"field"`
	Rows  []analyticsRow `json:"rows"`
	// Truncated says the dimension had more values than we returned, so the UI can say
	// "top 6" instead of implying it is the whole story.
	Truncated bool `json:"truncated"`
}

// analyticsMatrix is one cross-tab: the same events counted against two dimensions at
// once. Two breakdowns side by side cannot answer "is staging noisy, or is staging where
// the warnings live" — only their intersection can.
type analyticsMatrix struct {
	RowField    string   `json:"row_field"`
	ColumnField string   `json:"column_field"`
	Rows        []string `json:"rows"`
	Columns     []string `json:"columns"`
	// Cells is row-major, zero-filled: a pair with no events is a zero, not a gap.
	Cells [][]int64 `json:"cells"`
}

// analyticsWindow is a window reduced to its headline figures and its level split. Used
// for the previous period, which is what turns "180 events" into "180 events, up from 24"
// — and, per level, tells a wall of new warnings apart from a wall of new errors.
type analyticsWindow struct {
	Start  time.Time        `json:"start"`
	End    time.Time        `json:"end"`
	Total  int64            `json:"total"`
	Issues int64            `json:"issues"`
	Levels []analyticsLevel `json:"levels"`
}

type analyticsIssue struct {
	ID       int64     `json:"id"`
	Title    string    `json:"title"`
	Culprit  string    `json:"culprit"`
	Level    string    `json:"level"`
	Count    int64     `json:"count"`
	LastSeen time.Time `json:"last_seen"`
}

// handleProjectAnalytics answers GET /api/0/analytics?project=<slug>. It takes the same
// window parameters as every other stats endpoint — statsPeriod, or start/end — so a link
// to a window means the same thing wherever it is pasted.
func (s *Server) handleProjectAnalytics(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "sign in, or pass an ingest key")
		return
	}
	slug := strings.TrimSpace(r.URL.Query().Get("project"))
	if slug == "" {
		writeErr(w, http.StatusBadRequest, "project is required")
		return
	}
	ids, err := s.projectIDsForSlugs(r, []string{slug})
	if err != nil {
		var unknown *unknownProjectError
		if errors.As(err, &unknown) {
			writeErr(w, http.StatusNotFound, "no such app")
			return
		}
		s.log.Error("project lookup failed", "slug", slug, "err", err)
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}

	ctx := r.Context()
	from, to := sentryWindow(r.URL.Query())
	filter := events.Search{ProjectIDs: ids}

	levels, total, issues, err := s.levelSplit(ctx, filter, from, to)
	if err != nil {
		s.log.Error("project analytics levels failed", "slug", slug, "err", err)
		writeErr(w, http.StatusServiceUnavailable, "could not read the level breakdown")
		return
	}

	// The window immediately before this one, same length. A count on its own says
	// nothing about direction — 180 errors is reassuring after 900 and alarming after 4.
	span := to.Sub(from)
	prev := analyticsWindow{Start: from.Add(-span), End: from}
	prev.Levels, prev.Total, prev.Issues, err = s.levelSplit(ctx, filter, prev.Start, prev.End)
	if err != nil {
		s.log.Error("project analytics comparison failed", "slug", slug, "err", err)
		writeErr(w, http.StatusServiceUnavailable, "could not read the previous window")
		return
	}

	breakdowns := make([]analyticsBreakdown, 0, len(analyticsDimensions))
	for _, field := range analyticsDimensions {
		b, err := s.breakdown(ctx, filter, from, to, field)
		if err != nil {
			s.log.Error("project analytics breakdown failed", "slug", slug, "field", field, "err", err)
			writeErr(w, http.StatusServiceUnavailable, "could not read the "+field+" breakdown")
			return
		}
		// A dimension nobody sets is not a panel. An SDK that never sends a release tag
		// would otherwise get a "Release" card whose only row is "(none): everything".
		if len(b.Rows) == 0 || (len(b.Rows) == 1 && b.Rows[0].Value == "") {
			continue
		}
		breakdowns = append(breakdowns, b)
	}

	tagged, err := s.tagDimensions(ctx, filter, from, to)
	if err != nil {
		s.log.Error("project analytics tag dimensions failed", "slug", slug, "err", err)
		writeErr(w, http.StatusServiceUnavailable, "could not read the tag breakdowns")
		return
	}
	breakdowns = append(breakdowns, tagged...)

	matrix, err := s.matrix(ctx, filter, from, to, "environment", "level")
	if err != nil {
		s.log.Error("project analytics matrix failed", "slug", slug, "err", err)
		writeErr(w, http.StatusServiceUnavailable, "could not cross-tabulate the window")
		return
	}

	issueRows, err := s.events.DiscoverEvents(ctx, events.EventSearch{
		Search: filter, From: from, To: to,
		Fields: []string{"issue.id", "title", "culprit", "level", "count()", "last_seen()"},
		Sort:   "-count()", Limit: topIssueRows,
	})
	if err != nil {
		s.log.Error("project analytics top issues failed", "slug", slug, "err", err)
		writeErr(w, http.StatusServiceUnavailable, "could not read the busiest issues")
		return
	}
	top := make([]analyticsIssue, 0, len(issueRows))
	for _, row := range issueRows {
		id, err := strconv.ParseInt(rowString(row["issue.id"]), 10, 64)
		if err != nil {
			continue // without an id the row is a dead end in the UI: no link to follow
		}
		top = append(top, analyticsIssue{
			ID:       id,
			Title:    rowString(row["title"]),
			Culprit:  rowString(row["culprit"]),
			Level:    rowString(row["level"]),
			Count:    rowInt(row["count()"]),
			LastSeen: rowTime(row["last_seen()"]),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"project":    slug,
		"start":      from,
		"end":        to,
		"total":      total,
		"issues":     issues,
		"previous":   prev,
		"levels":     levels,
		"breakdowns": breakdowns,
		"matrix":     matrix,
		"top_issues": top,
	})
}

// levelSplit groups a window by level, and returns the window's totals with it. The
// totals are derived from the split rather than counted separately because level is a
// property of the issue: the groups partition the window exactly, so a figure summed from
// them can never contradict the split shown beside it.
func (s *Server) levelSplit(
	ctx context.Context, filter events.Search, from, to time.Time,
) (levels []analyticsLevel, total, issues int64, err error) {
	rows, err := s.events.DiscoverEvents(ctx, events.EventSearch{
		Search: filter, From: from, To: to,
		Fields: []string{"level", "count()", "count_unique(issue.id)"},
		Sort:   "-count()", Limit: maxLevelRows,
	})
	if err != nil {
		return nil, 0, 0, err
	}
	levels = make([]analyticsLevel, 0, len(rows))
	for _, row := range rows {
		l := analyticsLevel{
			Level:  rowString(row["level"]),
			Count:  rowInt(row["count()"]),
			Issues: rowInt(row["count_unique(issue.id)"]),
		}
		total += l.Count
		issues += l.Issues
		levels = append(levels, l)
	}
	return levels, total, issues, nil
}

// tagDimensions discovers which of the project's own tags are worth a panel, and breaks
// the window down by each. Discovery rather than a fixed list because ingest keeps every
// tag an SDK sends: what those are is the project's business, not ours.
//
// A tag is skipped when a fixed dimension already answers it, when it looks like an
// identifier (too many distinct values to summarise), or when it resolves to fewer than
// two rows in this window — a panel reading "one value, 100%" is a panel that says nothing.
func (s *Server) tagDimensions(
	ctx context.Context, filter events.Search, from, to time.Time,
) ([]analyticsBreakdown, error) {
	keys, err := s.events.TagKeys(ctx)
	if err != nil {
		return nil, err
	}
	// Widest first: a tag with more distinct values splits the window more finely, which
	// is what makes it interesting. TagKeys returns them alphabetically.
	sort.SliceStable(keys, func(a, b int) bool { return keys[a].Values > keys[b].Values })

	out := []analyticsBreakdown{}
	for _, k := range keys {
		if len(out) == tagDimensionCount {
			break
		}
		if fixedTagKeys[k.Key] || k.Values > maxTagCardinality || k.Values < 2 {
			continue
		}
		b, err := s.breakdown(ctx, filter, from, to, "tags["+k.Key+"]")
		if err != nil {
			return nil, err
		}
		if len(b.Rows) < 2 || dominatedByUnset(b) {
			continue
		}
		out = append(out, b)
	}
	return out, nil
}

// dominatedByUnset reports whether a discovered tag is mostly absent. Measured on real
// data: `url` is set on three events out of 196, so its panel was one full-width "(not
// set)" bar and two slivers — true, and useless. A fixed dimension keeps its unset row
// (that a tenth of events carry no release is worth seeing); a tag we chose to show only
// because it exists does not.
func dominatedByUnset(b analyticsBreakdown) bool {
	var total, unset int64
	for _, r := range b.Rows {
		total += r.Count
		if r.Value == "" {
			unset = r.Count
		}
	}
	return total > 0 && unset*10 >= total*9
}

// matrix cross-tabulates the window against two fields, pivoted here rather than in the
// browser so the axes are ordered by weight and the zero-filling is not the UI's problem.
func (s *Server) matrix(
	ctx context.Context, filter events.Search, from, to time.Time, rowField, colField string,
) (analyticsMatrix, error) {
	out := analyticsMatrix{
		RowField: rowField, ColumnField: colField,
		Rows: []string{}, Columns: []string{}, Cells: [][]int64{},
	}
	rows, err := s.events.DiscoverEvents(ctx, events.EventSearch{
		Search: filter, From: from, To: to,
		Fields: []string{rowField, colField, "count()"},
		Sort:   "-count()", Limit: matrixCells,
	})
	if err != nil {
		return out, err
	}

	type cell struct{ row, col string }
	counts := map[cell]int64{}
	rowTotals, colTotals := map[string]int64{}, map[string]int64{}
	for _, r := range rows {
		c := cell{rowString(r[rowField]), rowString(r[colField])}
		n := rowInt(r["count()"])
		counts[c] += n
		rowTotals[c.row] += n
		colTotals[c.col] += n
	}
	out.Rows, out.Columns = byWeight(rowTotals, matrixRows), byWeight(colTotals, matrixColumns)
	for _, row := range out.Rows {
		line := make([]int64, 0, len(out.Columns))
		for _, col := range out.Columns {
			line = append(line, counts[cell{row, col}])
		}
		out.Cells = append(out.Cells, line)
	}
	return out, nil
}

// byWeight orders an axis biggest first, name as the tiebreak so a redraw does not
// reshuffle it, and caps its length.
func byWeight(totals map[string]int64, limit int) []string {
	keys := make([]string, 0, len(totals))
	for k := range totals {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool {
		if totals[keys[a]] != totals[keys[b]] {
			return totals[keys[a]] > totals[keys[b]]
		}
		return keys[a] < keys[b]
	})
	if len(keys) > limit {
		keys = keys[:limit]
	}
	return keys
}

// breakdown groups the window by one Discover field, biggest first. It asks for one row
// more than it returns, which is how Truncated is known without a second count query.
func (s *Server) breakdown(
	ctx context.Context, filter events.Search, from, to time.Time, field string,
) (analyticsBreakdown, error) {
	out := analyticsBreakdown{Field: field, Rows: []analyticsRow{}}
	rows, err := s.events.DiscoverEvents(ctx, events.EventSearch{
		Search: filter, From: from, To: to,
		Fields: []string{field, "count()", "count_unique(issue.id)"},
		Sort:   "-count()", Limit: breakdownRows + 1,
	})
	if err != nil {
		return out, err
	}
	if len(rows) > breakdownRows {
		out.Truncated = true
		rows = rows[:breakdownRows]
	}
	for _, row := range rows {
		out.Rows = append(out.Rows, analyticsRow{
			Value:  rowString(row[field]),
			Count:  rowInt(row["count()"]),
			Issues: rowInt(row["count_unique(issue.id)"]),
		})
	}
	return out, nil
}

// rowInt, rowString and rowTime flatten what pgx hands back through Discover's
// map[string]any rows. Discover is loosely typed on purpose — its columns are named by
// the caller — so the typing happens here, in the one place that knows the shape.
func rowInt(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int32:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}

func rowString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func rowTime(v any) time.Time {
	if t, ok := v.(time.Time); ok {
		return t
	}
	return time.Time{}
}
