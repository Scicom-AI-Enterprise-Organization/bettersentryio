// Sentry-shaped reads: the other half of D14. Ingest already speaks Sentry's
// protocol so an SDK needs no code change; these queries answer what Sentry's
// *readers* ask for, which is what lets Grafana's official Sentry datasource
// query us with no plugin of our own. See docs/design/grafana-datasource.md.
//
// Everything here is a deliberate subset. A filter we cannot answer is an error,
// never a no-op: a Grafana panel that quietly ignores `level:error` does not look
// broken, it looks like there are no errors.
package events

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

/* ---- shared filters -------------------------------------------------------- */

// Search is the filter set every Sentry-shaped read shares.
type Search struct {
	ProjectIDs   []int64
	Environments []string
	// Status is Sentry's `is:` term: unresolved, resolved or archived. Empty means any.
	Status string
	Level  string
	// Kind is the exception type — Sentry's error.type. Not a tag: it is a column,
	// and looking it up in tags would silently match nothing.
	Kind string
	// IssueIDs answers `issue.id:` searches and the drill-down links a dashboard
	// panel builds.
	IssueIDs []int64
	// Tags covers release:, transaction:, server_name: and every client tag,
	// because ingest merges all of them into issues.tags.
	Tags map[string]string
	// Text is the bare words of a query, matched against the issue title.
	Text []string
}

// archivedSQL: a null archived_until means archived forever, a past one means the
// archive has expired and the issue is live again.
const archivedSQL = `(i.archived_at is not null and (i.archived_until is null or i.archived_until > now()))`

// arg appends a bind value and returns its placeholder. Every fragment below binds
// rather than interpolates — the search text arrives from a Grafana panel.
func arg(args *[]any, v any) string {
	*args = append(*args, v)
	return "$" + strconv.Itoa(len(*args))
}

// clauses renders the filters as " and …" fragments over the issues alias i and the
// projects alias p. The caller owns the leading where.
func (f Search) clauses(args *[]any) string {
	var b strings.Builder
	if len(f.ProjectIDs) > 0 {
		b.WriteString(" and p.id = any(" + arg(args, f.ProjectIDs) + ")")
	}
	if len(f.Environments) > 0 {
		b.WriteString(" and i.environment = any(" + arg(args, f.Environments) + ")")
	}
	switch f.Status {
	case "unresolved":
		b.WriteString(" and i.resolved_at is null and not " + archivedSQL)
	case "resolved":
		b.WriteString(" and i.resolved_at is not null")
	case "archived":
		b.WriteString(" and " + archivedSQL)
	}
	if f.Level != "" {
		b.WriteString(" and i.level = " + arg(args, f.Level))
	}
	if f.Kind != "" {
		b.WriteString(" and i.kind = " + arg(args, f.Kind))
	}
	if len(f.IssueIDs) > 0 {
		b.WriteString(" and i.id = any(" + arg(args, f.IssueIDs) + ")")
	}
	// Sorted so the same filter always generates the same SQL, and so Postgres can
	// reuse the plan instead of seeing a new statement per map iteration order.
	for _, k := range sortedKeys(f.Tags) {
		b.WriteString(" and i.tags->>" + arg(args, k) + " = " + arg(args, f.Tags[k]))
	}
	for _, w := range f.Text {
		b.WriteString(" and i.title ilike " + arg(args, "%"+w+"%"))
	}
	return b.String()
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

/* ---- projects -------------------------------------------------------------- */

// ProjectRef is a project as the Sentry API describes it: what an app is called and
// nothing about its health. The health view is the UI's job (monitor.App).
type ProjectRef struct {
	ID        int64
	Slug      string
	Name      string
	Platform  string
	CreatedAt time.Time
	// Environments is every environment we have seen reporting, errors and
	// heartbeats alike — it populates the datasource's environment picker.
	Environments []string
}

func (s *Store) Projects(ctx context.Context) ([]ProjectRef, error) {
	rows, err := s.db.Query(ctx, `
		select id, slug, name, platform, created_at from projects order by created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ProjectRef{}
	byID := map[int64]int{}
	for rows.Next() {
		var p ProjectRef
		if err := rows.Scan(&p.ID, &p.Slug, &p.Name, &p.Platform, &p.CreatedAt); err != nil {
			return nil, err
		}
		byID[p.ID] = len(out)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// One grouped query for every project's environments, never one per project.
	envs, err := s.db.Query(ctx, `
		select project_id, environment from (
		    select project_id, environment from issues
		    union
		    select m.project_id, ms.environment
		      from monitor_state ms join monitors m on m.id = ms.monitor_id
		) e
		where environment <> ''
		order by project_id, environment`)
	if err != nil {
		return nil, err
	}
	defer envs.Close()
	for envs.Next() {
		var id int64
		var env string
		if err := envs.Scan(&id, &env); err != nil {
			return nil, err
		}
		if n, ok := byID[id]; ok {
			out[n].Environments = append(out[n].Environments, env)
		}
	}
	return out, envs.Err()
}

/* ---- issues ---------------------------------------------------------------- */

// IssueRow is one issue as Sentry reports it: lifetime counts plus the counts
// scoped to the requested window. Sentry's own UI shows both — the window count is
// what a dashboard graphs, the lifetime dates are what a human reads.
type IssueRow struct {
	ID              int64
	ProjectID       int64
	ProjectSlug     string
	ProjectName     string
	ProjectPlatform string
	Title           string
	Culprit         string
	Kind            string
	Level           string
	Environment     string
	Tags            map[string]string
	Resolved        bool
	Archived        bool
	TimesSeen       int64
	FirstSeen       time.Time
	LastSeen        time.Time
	WindowCount     int64
	WindowFirstSeen time.Time
	WindowLastSeen  time.Time
}

type IssueSearch struct {
	Search
	From, To time.Time
	// Sort is Sentry's issue sort: date, new, freq or user.
	Sort  string
	Limit int
}

// SearchIssues answers /organizations/{org}/issues/. Only issues with at least one
// event inside the window are returned, which is what Sentry does and what makes a
// dashboard's time picker mean anything.
func (s *Store) SearchIssues(ctx context.Context, q IssueSearch) ([]IssueRow, error) {
	if q.Limit <= 0 || q.Limit > 1000 {
		q.Limit = 100
	}
	args := []any{}
	from, to := arg(&args, q.From), arg(&args, q.To)
	// The lifetime overlap test is a cheap prefilter on indexed columns: an issue
	// whose whole life sits outside the window cannot have an event inside it, so
	// the per-issue lateral count never runs for it.
	where := "where i.last_seen >= " + from + " and i.first_seen < " + to + q.clauses(&args)

	order := "w.last desc"
	switch q.Sort {
	case "new":
		order = "i.first_seen desc"
	case "freq", "user": // no user tracking; frequency is the honest answer for both
		order = "w.count desc"
	}

	rows, err := s.db.Query(ctx, `
		select i.id, p.id, p.slug, p.name, p.platform,
		       i.title, i.culprit, i.kind, i.level, i.environment, i.tags,
		       i.resolved_at is not null, `+archivedSQL+`,
		       i.times_seen, i.first_seen, i.last_seen,
		       w.count, w.first, w.last
		from issues i
		join projects p on p.id = i.project_id
		join lateral (
		    select count(*) as count, min(e.received_at) as first, max(e.received_at) as last
		    from events e
		    where e.issue_id = i.id and e.received_at >= `+from+` and e.received_at < `+to+`
		) w on w.count > 0
		`+where+`
		order by `+order+`
		limit `+arg(&args, q.Limit), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []IssueRow{}
	for rows.Next() {
		var r IssueRow
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.ProjectSlug, &r.ProjectName, &r.ProjectPlatform,
			&r.Title, &r.Culprit, &r.Kind, &r.Level, &r.Environment, &r.Tags,
			&r.Resolved, &r.Archived, &r.TimesSeen, &r.FirstSeen, &r.LastSeen,
			&r.WindowCount, &r.WindowFirstSeen, &r.WindowLastSeen); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

/* ---- discover fields ------------------------------------------------------- */

// discoverFields maps Sentry Discover field names onto SQL over events e, issues i
// and projects p. Only fields we can answer truthfully are here; anything else is
// rejected by fieldSQL with the name in the message, so a mistyped field in a panel
// says so instead of returning an empty column.
var discoverFields = map[string]string{
	"id":           "coalesce(nullif(e.payload->>'event_id', ''), e.id::text)",
	"event.id":     "coalesce(nullif(e.payload->>'event_id', ''), e.id::text)",
	"timestamp":    "e.received_at",
	"message":      "e.message",
	"title":        "i.title",
	"culprit":      "i.culprit",
	"level":        "i.level",
	"error.type":   "i.kind",
	"environment":  "i.environment",
	"release":      "i.tags->>'release'",
	"transaction":  "i.tags->>'transaction'",
	"server_name":  "i.tags->>'server_name'",
	"platform":     "p.platform",
	"project":      "p.slug",
	"project.id":   "p.id::text",
	"project.name": "p.name",
	"issue":        "upper(p.slug) || '-' || i.id::text",
	"issue.id":     "i.id::text",
}

// fieldSQL resolves one selectable field. `tags[whatever]` is resolved dynamically
// because ingest keeps every client tag, so the set is open.
func fieldSQL(name string, args *[]any) (string, error) {
	name = strings.TrimSpace(name)
	if key, ok := tagField(name); ok {
		return "i.tags->>" + arg(args, key), nil
	}
	if expr, ok := discoverFields[name]; ok {
		return expr, nil
	}
	return "", fmt.Errorf("unsupported field %q", name)
}

func tagField(name string) (string, bool) {
	if strings.HasPrefix(name, "tags[") && strings.HasSuffix(name, "]") {
		return name[len("tags[") : len(name)-1], true
	}
	return "", false
}

// aggregateSQL resolves the aggregate functions a dashboard actually uses. It
// reports ok=false for a plain field name so callers can tell the two apart.
func aggregateSQL(name string, args *[]any) (expr string, ok bool, err error) {
	name = strings.TrimSpace(name)
	open := strings.Index(name, "(")
	if open < 0 || !strings.HasSuffix(name, ")") {
		return "", false, nil
	}
	fn, inner := name[:open], strings.TrimSpace(name[open+1:len(name)-1])
	switch fn {
	case "count":
		if inner != "" {
			return "", true, fmt.Errorf("count() takes no argument, got %q", inner)
		}
		return "count(*)", true, nil
	case "count_unique":
		// count_unique(issue) is the one every "how many distinct bugs" panel uses.
		sub, err := fieldSQL(inner, args)
		if err != nil {
			return "", true, err
		}
		return "count(distinct " + sub + ")", true, nil
	case "last_seen":
		return "max(e.received_at)", true, nil
	case "first_seen":
		return "min(e.received_at)", true, nil
	}
	return "", true, fmt.Errorf("unsupported aggregate %q", name)
}

/* ---- discover events ------------------------------------------------------- */

type EventSearch struct {
	Search
	From, To time.Time
	Fields   []string
	Sort     string
	Limit    int
}

// DiscoverEvents answers /organizations/{org}/events/ — Sentry's Discover table.
// Fields are returned keyed by the name that was asked for, aggregates group by
// every plain field selected alongside them, exactly as Discover does.
func (s *Store) DiscoverEvents(ctx context.Context, q EventSearch) ([]map[string]any, error) {
	if len(q.Fields) == 0 {
		q.Fields = []string{"id", "title", "timestamp", "project", "level"}
	}
	if q.Limit <= 0 || q.Limit > 1000 {
		q.Limit = 100
	}

	args := []any{}
	from, to := arg(&args, q.From), arg(&args, q.To)

	var (
		selects   []string
		grouped   []string // select positions to group by
		aggAt     = -1     // position of the first aggregate, for the default sort
		positions = map[string]int{}
	)
	for _, f := range q.Fields {
		expr, isAgg, err := aggregateSQL(f, &args)
		if err != nil {
			return nil, err
		}
		if !isAgg {
			if expr, err = fieldSQL(f, &args); err != nil {
				return nil, err
			}
		}
		selects = append(selects, expr)
		positions[f] = len(selects)
		if isAgg {
			if aggAt < 0 {
				aggAt = len(selects)
			}
		} else {
			grouped = append(grouped, strconv.Itoa(len(selects)))
		}
	}

	query := "select " + strings.Join(selects, ", ") + `
		from events e
		join issues i on i.id = e.issue_id
		join projects p on p.id = i.project_id
		where e.received_at >= ` + from + " and e.received_at < " + to + q.clauses(&args)
	if aggAt > 0 && len(grouped) > 0 {
		query += " group by " + strings.Join(grouped, ", ")
	}
	query += " order by " + q.orderBy(positions, aggAt) + " limit " + arg(&args, q.Limit)

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []map[string]any{}
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		row := make(map[string]any, len(q.Fields))
		for n, f := range q.Fields {
			if n < len(vals) {
				row[f] = vals[n]
			}
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// orderBy sorts by select position, never by a bare column: with an aggregate in
// the list, anything outside the group by is not orderable.
func (q EventSearch) orderBy(positions map[string]int, aggAt int) string {
	name, dir := strings.TrimPrefix(q.Sort, "-"), "asc"
	if strings.HasPrefix(q.Sort, "-") || q.Sort == "" {
		dir = "desc"
	}
	if pos, ok := positions[name]; ok {
		return strconv.Itoa(pos) + " " + dir
	}
	if aggAt > 0 {
		return strconv.Itoa(aggAt) + " desc"
	}
	if pos, ok := positions["timestamp"]; ok {
		return strconv.Itoa(pos) + " desc"
	}
	return "e.received_at desc"
}

/* ---- time series ----------------------------------------------------------- */

// Series is one line on a graph. Group keeps the values that named it, so callers
// that need Sentry's `by` object (stats_v2) can rebuild it.
type Series struct {
	Name   string
	Group  map[string]string
	Values []float64
}

// SeriesSet is a set of series over one shared, zero-filled bucket axis. Grafana
// needs the gaps: a bucket with no events is a zero, not a missing point.
type SeriesSet struct {
	Buckets []time.Time
	Series  []Series
	// Interval is the width actually used, which is not always the width asked for:
	// a request for a year of one-minute buckets is widened, and a caller that
	// labels its axis has to be told.
	Interval time.Duration
}

type StatsSearch struct {
	Search
	From, To time.Time
	Interval time.Duration
	// YAxis defaults to count(). Anything aggregateSQL accepts works.
	YAxis   string
	GroupBy []string
	// Top caps how many series come back, biggest total first — Sentry's topEvents.
	Top int
}

// maxBuckets caps the axis. A panel asking for a year at one-minute resolution is a
// mistake we widen rather than answer: half a million points helps nobody.
const maxBuckets = 5000

// EventSeries answers /organizations/{org}/events-stats/ and, reshaped by the
// caller, /organizations/{org}/stats_v2/.
func (s *Store) EventSeries(ctx context.Context, q StatsSearch) (SeriesSet, error) {
	set := SeriesSet{}
	if !q.To.After(q.From) {
		return set, fmt.Errorf("end must be after start")
	}
	if q.Interval <= 0 {
		q.Interval = time.Minute
	}
	if span := q.To.Sub(q.From); span/q.Interval > maxBuckets {
		q.Interval = (span / maxBuckets).Round(time.Second)
		if q.Interval <= 0 {
			q.Interval = time.Second
		}
	}
	if q.YAxis == "" {
		q.YAxis = "count()"
	}
	if q.Top <= 0 || q.Top > 100 {
		q.Top = 10
	}

	set.Interval = q.Interval
	// Both sides must count from the same instant or the join by bucket silently
	// drops everything. date_bin counts from its origin; time.Truncate counts from
	// year 1, which only coincides with the epoch for intervals that divide a day —
	// so the axis is aligned against the epoch explicitly. (Measured: a 7h12m auto
	// interval put every Postgres bucket off the Go axis and the chart read zero.)
	origin := time.Unix(0, 0).UTC()
	for t := alignDown(q.From, q.Interval); t.Before(q.To); t = t.Add(q.Interval) {
		set.Buckets = append(set.Buckets, t)
	}
	if len(set.Buckets) == 0 {
		return set, nil
	}
	index := make(map[time.Time]int, len(set.Buckets))
	for n, b := range set.Buckets {
		index[b] = n
	}

	args := []any{}
	interval := arg(&args, fmt.Sprintf("%d microseconds", q.Interval.Microseconds()))
	originArg := arg(&args, origin)
	agg, isAgg, err := aggregateSQL(q.YAxis, &args)
	if err != nil {
		return set, err
	}
	if !isAgg {
		return set, fmt.Errorf("yAxis %q is not an aggregate", q.YAxis)
	}

	selects := []string{"date_bin(" + interval + "::interval, e.received_at, " + originArg + ")"}
	groupExprs := []string{"1"}
	for _, g := range q.GroupBy {
		expr, err := fieldSQL(g, &args)
		if err != nil {
			return set, err
		}
		selects = append(selects, "coalesce("+expr+", '')")
		groupExprs = append(groupExprs, strconv.Itoa(len(selects)))
	}
	selects = append(selects, agg)

	from, to := arg(&args, set.Buckets[0]), arg(&args, q.To)
	rows, err := s.db.Query(ctx, "select "+strings.Join(selects, ", ")+`
		from events e
		join issues i on i.id = e.issue_id
		join projects p on p.id = i.project_id
		where e.received_at >= `+from+" and e.received_at < "+to+q.clauses(&args)+`
		group by `+strings.Join(groupExprs, ", "), args...)
	if err != nil {
		return set, err
	}
	defer rows.Close()

	type acc struct {
		group  map[string]string
		values []float64
		total  float64
	}
	series := map[string]*acc{}
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return set, err
		}
		bucket, ok := vals[0].(time.Time)
		if !ok {
			return set, fmt.Errorf("unexpected bucket type %T", vals[0])
		}
		slot, ok := index[bucket.UTC()]
		if !ok {
			continue // a boundary bucket outside the axis; the axis is authoritative
		}

		names := make([]string, 0, len(q.GroupBy))
		group := map[string]string{}
		for n, g := range q.GroupBy {
			v := fmt.Sprint(vals[1+n])
			group[g] = v
			names = append(names, v)
		}
		key := strings.Join(names, ", ")
		a := series[key]
		if a == nil {
			a = &acc{group: group, values: make([]float64, len(set.Buckets))}
			series[key] = a
		}
		v := numeric(vals[len(vals)-1])
		a.values[slot] += v
		a.total += v
	}
	if err := rows.Err(); err != nil {
		return set, err
	}

	keys := make([]string, 0, len(series))
	for k := range series {
		keys = append(keys, k)
	}
	// Biggest first, name as the tiebreak so a redraw does not reshuffle the legend.
	sort.Slice(keys, func(a, b int) bool {
		if series[keys[a]].total != series[keys[b]].total {
			return series[keys[a]].total > series[keys[b]].total
		}
		return keys[a] < keys[b]
	})
	if len(q.GroupBy) > 0 && len(keys) > q.Top {
		keys = keys[:q.Top]
	}
	for _, k := range keys {
		set.Series = append(set.Series, Series{Name: k, Group: series[k].group, Values: series[k].values})
	}
	// An ungrouped query still has to return its (possibly all-zero) line.
	if len(q.GroupBy) == 0 && len(set.Series) == 0 {
		set.Series = []Series{{Values: make([]float64, len(set.Buckets))}}
	}
	return set, nil
}

// alignDown floors t to a multiple of d counted from the Unix epoch — the same
// boundaries date_bin(d, ts, epoch) lands on.
func alignDown(t time.Time, d time.Duration) time.Time {
	r := t.UnixNano() % int64(d)
	if r < 0 { // pre-epoch: floor, not truncate-toward-zero
		r += int64(d)
	}
	return time.Unix(0, t.UnixNano()-r).UTC()
}

// numeric flattens whatever pgx hands back for an aggregate. count() is int64,
// last_seen() is a timestamp, and a graph wants a number either way.
func numeric(v any) float64 {
	switch n := v.(type) {
	case int64:
		return float64(n)
	case int32:
		return float64(n)
	case float64:
		return n
	case float32:
		return float64(n)
	case time.Time:
		return float64(n.UnixMilli())
	case nil:
		return 0
	}
	return 0
}

/* ---- tags ------------------------------------------------------------------ */

// Tag is one tag key and how many distinct values it has, for the datasource's tag
// picker. Derived from issues.tags, which is the merge of client and server tags.
type Tag struct {
	Key    string
	Values int
}

func (s *Store) TagKeys(ctx context.Context) ([]Tag, error) {
	rows, err := s.db.Query(ctx, `
		select key, count(distinct value) from (
		    select t.key, t.value from issues i, jsonb_each_text(i.tags) as t(key, value)
		) x
		group by key
		order by key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Tag{}
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.Key, &t.Values); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// IssueOfEvent resolves an event id — ours or the SDK's uuid — to its issue and
// project, so a link built from an event lands on the page that explains it.
func (s *Store) IssueOfEvent(ctx context.Context, eventID string) (issueID int64, projectSlug string, ok bool, err error) {
	err = s.db.QueryRow(ctx, `
		select i.id, p.slug
		from events e
		join issues i on i.id = e.issue_id
		join projects p on p.id = i.project_id
		where e.payload->>'event_id' = $1 or e.id::text = $1
		order by e.received_at desc
		limit 1`, eventID).Scan(&issueID, &projectSlug)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, err
	}
	return issueID, projectSlug, true, nil
}
