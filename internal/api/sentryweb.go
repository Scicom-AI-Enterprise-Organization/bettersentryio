// The Sentry *Web* API, read-only: the endpoints Sentry's own dashboards call, and
// therefore the endpoints Grafana's official Sentry datasource calls. Ingest already
// speaks Sentry's protocol (D14) so services need no code change; this is the same
// bargain for dashboards — point grafana-sentry-datasource at us and it works, with
// no plugin of ours to maintain. See docs/design/grafana-datasource.md.
//
// It is a subset, and the shape of the subset is the design: issues, discover events,
// event time series and outcome stats — the four things a dashboard actually draws.
// Tracing, spans, metrics and release sessions are answered with a reason, not a 404,
// because "bettersentryio does not do tracing" is information and a 404 is not.
package api

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/events"
)

// sentryOrg is the one organization this install presents. bettersentryio has no
// orgs or teams (PLAN: "what it will never do"), but the datasource requires a slug,
// so we answer as a single org — and accept any slug in the path, because a
// single-tenant install refusing a typo'd org name helps nobody.
const sentryOrg = "bettersentryio"

// sentryTeam is the synthetic team every project belongs to. Same reasoning: the
// datasource's team picker needs something to pick.
const sentryTeam = "engineering"

func (s *Server) sentryWebRoutes(mux *http.ServeMux) {
	routes := map[string]http.HandlerFunc{
		"/api/0/organizations":                    s.handleSentryOrgs,
		"/api/0/organizations/{org}":              s.handleSentryOrg,
		"/api/0/organizations/{org}/projects":     s.handleSentryProjects,
		"/api/0/organizations/{org}/teams":        s.handleSentryTeams,
		"/api/0/organizations/{org}/tags":         s.handleSentryTags,
		"/api/0/organizations/{org}/issues":       s.handleSentryIssues,
		"/api/0/organizations/{org}/events":       s.handleSentryEvents,
		"/api/0/organizations/{org}/events-stats": s.handleSentryEventsStats,
		"/api/0/organizations/{org}/stats_v2":     s.handleSentryStatsV2,
		"/api/0/teams/{org}/{team}/projects":      s.handleSentryProjects,
		// Deliberately unimplemented, answered with the reason.
		"/api/0/organizations/{org}/metrics/data":           s.handleSentryUnsupported,
		"/api/0/organizations/{org}/trace-items/attributes": s.handleSentryUnsupported,
	}
	for path, h := range routes {
		// Sentry answers with and without the trailing slash; the plugin's queries
		// send one and its resource calls do not.
		mux.HandleFunc("GET "+path, h)
		mux.HandleFunc("GET "+path+"/{$}", h)
	}
	// Grafana builds its "Open in Sentry" data links from the datasource URL, so they
	// arrive here. Send them to the page that explains the issue.
	mux.HandleFunc("GET /organizations/{org}/issues/{id}/{$}", s.handleSentryIssueLink)
	mux.HandleFunc("GET /organizations/{org}/discover/{ref}/{$}", s.handleSentryEventLink)
}

/* ---- plumbing -------------------------------------------------------------- */

// sentryErr answers in Sentry's error shape: the datasource decodes `detail` and
// shows it to the operator verbatim, so this is the only channel we have for saying
// what went wrong.
func sentryErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"detail": msg})
}

// sentryRead gates the whole surface. Reads take the operator token or any ingest
// key — both as a bearer token, because a Grafana datasource holds one credential
// and sends it one way. Nothing here writes, so an ingest key is enough.
func (s *Server) sentryRead(w http.ResponseWriter, r *http.Request) bool {
	if s.authorized(r) {
		return true
	}
	sentryErr(w, http.StatusUnauthorized, "invalid token: send the operator token or an app's ingest key as a bearer token")
	return false
}

/* ---- organizations, projects, teams, tags ---------------------------------- */

type sentryRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type sentryOrgJSON struct {
	ID             string    `json:"id"`
	Slug           string    `json:"slug"`
	Name           string    `json:"name"`
	DateCreated    time.Time `json:"dateCreated"`
	IsEarlyAdopter bool      `json:"isEarlyAdopter"`
	Require2FA     bool      `json:"require2FA"`
	Status         sentryRef `json:"status"`
}

func (s *Server) org() sentryOrgJSON {
	return sentryOrgJSON{
		ID: "1", Slug: sentryOrg, Name: sentryOrg, DateCreated: s.started,
		Status: sentryRef{ID: "active", Name: "active"},
	}
}

func (s *Server) handleSentryOrgs(w http.ResponseWriter, r *http.Request) {
	if !s.sentryRead(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, []sentryOrgJSON{s.org()})
}

func (s *Server) handleSentryOrg(w http.ResponseWriter, r *http.Request) {
	if !s.sentryRead(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, s.org())
}

type sentryProjectJSON struct {
	ID           string      `json:"id"`
	Slug         string      `json:"slug"`
	Name         string      `json:"name"`
	Platform     string      `json:"platform"`
	DateCreated  time.Time   `json:"dateCreated"`
	Environments []string    `json:"environments"`
	HasAccess    bool        `json:"hasAccess"`
	IsMember     bool        `json:"isMember"`
	IsBookmarked bool        `json:"isBookmarked"`
	Team         sentryRef   `json:"team"`
	Teams        []sentryRef `json:"teams"`
}

func (s *Server) handleSentryProjects(w http.ResponseWriter, r *http.Request) {
	if !s.sentryRead(w, r) {
		return
	}
	projects, err := s.events.Projects(r.Context())
	if err != nil {
		s.log.Error("sentry projects failed", "err", err)
		sentryErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	team := sentryRef{ID: "1", Name: sentryTeam, Slug: sentryTeam}
	out := make([]sentryProjectJSON, 0, len(projects))
	for _, p := range projects {
		envs := p.Environments
		if envs == nil {
			envs = []string{}
		}
		out = append(out, sentryProjectJSON{
			ID: strconv.FormatInt(p.ID, 10), Slug: p.Slug, Name: p.Name,
			Platform: p.Platform, DateCreated: p.CreatedAt, Environments: envs,
			HasAccess: true, IsMember: true, Team: team, Teams: []sentryRef{team},
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSentryTeams(w http.ResponseWriter, r *http.Request) {
	if !s.sentryRead(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, []map[string]any{{
		"id": "1", "slug": sentryTeam, "name": sentryTeam,
		"dateCreated": s.started, "hasAccess": true, "isMember": true,
		"isPending": false, "memberCount": 0, "projects": []any{},
	}})
}

func (s *Server) handleSentryTags(w http.ResponseWriter, r *http.Request) {
	if !s.sentryRead(w, r) {
		return
	}
	tags, err := s.events.TagKeys(r.Context())
	if err != nil {
		s.log.Error("sentry tags failed", "err", err)
		sentryErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	out := make([]map[string]any, 0, len(tags))
	for _, t := range tags {
		out = append(out, map[string]any{"key": t.Key, "name": t.Key, "totalValues": t.Values})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSentryUnsupported(w http.ResponseWriter, r *http.Request) {
	if !s.sentryRead(w, r) {
		return
	}
	sentryErr(w, http.StatusNotImplemented,
		"bettersentryio does not store tracing, spans or metrics (PLAN: what it will never do). "+
			"Use the issues, events or eventsStats query types.")
}

/* ---- issues ---------------------------------------------------------------- */

type sentryIssueJSON struct {
	ID        string `json:"id"`
	ShortID   string `json:"shortId"`
	Title     string `json:"title"`
	Culprit   string `json:"culprit"`
	Level     string `json:"level"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	Substatus string `json:"substatus"`
	Platform  string `json:"platform"`
	// Count is a string because Sentry's is: an int64 dressed as text, and the
	// datasource decodes it as text.
	Count       string    `json:"count"`
	UserCount   int64     `json:"userCount"`
	FirstSeen   time.Time `json:"firstSeen"`
	LastSeen    time.Time `json:"lastSeen"`
	IsUnhandled bool      `json:"isUnhandled"`
	Permalink   string    `json:"permalink"`
	Project     struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Slug     string `json:"slug"`
		Platform string `json:"platform"`
	} `json:"project"`
	Metadata struct {
		Value string `json:"value"`
		Type  string `json:"type"`
	} `json:"metadata"`
	// Lifetime is the whole-of-life count; the fields above are scoped to the
	// requested window. Sentry reports both and its UI shows the lifetime dates.
	Lifetime struct {
		Count     string    `json:"count"`
		UserCount int64     `json:"userCount"`
		FirstSeen time.Time `json:"firstSeen"`
		LastSeen  time.Time `json:"lastSeen"`
	} `json:"lifetime"`
}

func (s *Server) handleSentryIssues(w http.ResponseWriter, r *http.Request) {
	if !s.sentryRead(w, r) {
		return
	}
	q := r.URL.Query()
	search, err := s.sentrySearch(r, q.Get("query"))
	if err != nil {
		sentryErr(w, http.StatusBadRequest, err.Error())
		return
	}
	from, to := sentryWindow(q)
	rows, err := s.events.SearchIssues(r.Context(), events.IssueSearch{
		Search: search, From: from, To: to,
		Sort: q.Get("sort"), Limit: atoiOr(q.Get("limit"), 100),
	})
	if err != nil {
		s.log.Error("sentry issues failed", "err", err)
		sentryErr(w, http.StatusServiceUnavailable, "could not read issues: "+err.Error())
		return
	}

	out := make([]sentryIssueJSON, 0, len(rows))
	for _, i := range rows {
		var j sentryIssueJSON
		id := strconv.FormatInt(i.ID, 10)
		j.ID, j.ShortID = id, strings.ToUpper(i.ProjectSlug)+"-"+id
		j.Title, j.Culprit, j.Level, j.Type = i.Title, i.Culprit, i.Level, "error"
		j.Status, j.Substatus = sentryStatus(i)
		j.Platform = i.ProjectPlatform
		j.Count = strconv.FormatInt(i.WindowCount, 10)
		j.FirstSeen, j.LastSeen = i.WindowFirstSeen, i.WindowLastSeen
		// The handled tag is derived at ingest from the exception mechanism, so an
		// event that arrived through an exception hook is correctly "unhandled".
		j.IsUnhandled = i.Tags["handled"] == "no"
		j.Permalink = s.baseURL + "/apps/" + i.ProjectSlug + "/errors/" + id
		j.Project.ID = strconv.FormatInt(i.ProjectID, 10)
		j.Project.Name, j.Project.Slug, j.Project.Platform = i.ProjectName, i.ProjectSlug, i.ProjectPlatform
		j.Metadata.Type = i.Kind
		j.Metadata.Value = strings.TrimPrefix(i.Title, i.Kind+": ")
		j.Lifetime.Count = strconv.FormatInt(i.TimesSeen, 10)
		j.Lifetime.FirstSeen, j.Lifetime.LastSeen = i.FirstSeen, i.LastSeen
		out = append(out, j)
	}
	writeJSON(w, http.StatusOK, out)
}

// sentryStatus maps our two-axis state (resolved, archived) onto Sentry's wire
// names. Sentry renamed "ignored" to "archived" in the UI but kept the old value on
// the API, which is what clients still match on.
func sentryStatus(i events.IssueRow) (status, substatus string) {
	switch {
	case i.Resolved:
		return "resolved", ""
	case i.Archived:
		return "ignored", "archived_forever"
	}
	return "unresolved", "ongoing"
}

/* ---- discover events ------------------------------------------------------- */

func (s *Server) handleSentryEvents(w http.ResponseWriter, r *http.Request) {
	if !s.sentryRead(w, r) {
		return
	}
	q := r.URL.Query()
	search, err := s.sentrySearch(r, q.Get("query"))
	if err != nil {
		sentryErr(w, http.StatusBadRequest, err.Error())
		return
	}
	from, to := sentryWindow(q)
	rows, err := s.events.DiscoverEvents(r.Context(), events.EventSearch{
		Search: search, From: from, To: to,
		Fields: q["field"], Sort: q.Get("sort"),
		Limit: atoiOr(q.Get("per_page"), 100),
	})
	if err != nil {
		s.log.Warn("sentry events failed", "err", err)
		sentryErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": rows,
		"meta": map[string]any{"fields": discoverMeta(q["field"], rows)},
	})
}

// discoverMeta types each column the way Sentry does. The datasource ignores it, but
// anything else reading the endpoint by hand needs to know what a column holds.
func discoverMeta(fields []string, rows []map[string]any) map[string]string {
	out := map[string]string{}
	for _, f := range fields {
		out[f] = "string"
		for _, row := range rows {
			switch row[f].(type) {
			case time.Time:
				out[f] = "date"
			case int64, int32, int:
				out[f] = "integer"
			case float64, float32:
				out[f] = "number"
			default:
				continue
			}
			break
		}
	}
	return out
}

/* ---- events-stats ---------------------------------------------------------- */

func (s *Server) handleSentryEventsStats(w http.ResponseWriter, r *http.Request) {
	if !s.sentryRead(w, r) {
		return
	}
	q := r.URL.Query()
	search, err := s.sentrySearch(r, q.Get("query"))
	if err != nil {
		sentryErr(w, http.StatusBadRequest, err.Error())
		return
	}
	from, to := sentryWindow(q)

	// The datasource sends the y-axes it wants plus, in `field`, those same axes
	// followed by the group-by fields. The difference is the grouping.
	yAxis := q["yAxis"]
	if len(yAxis) == 0 {
		yAxis = []string{"count()"}
	}
	var groupBy []string
	for _, f := range q["field"] {
		if !contains(yAxis, f) {
			groupBy = append(groupBy, f)
		}
	}

	set, err := s.events.EventSeries(r.Context(), events.StatsSearch{
		Search: search, From: from, To: to,
		Interval: sentryInterval(q.Get("interval"), to.Sub(from)),
		YAxis:    yAxis[0], GroupBy: groupBy,
		Top: atoiOr(q.Get("topEvents"), 10),
	})
	if err != nil {
		s.log.Warn("sentry events-stats failed", "err", err)
		sentryErr(w, http.StatusBadRequest, err.Error())
		return
	}

	// Sentry's two shapes: one series is a bare {data: …}, several are keyed by
	// group name. The datasource walks both.
	if len(groupBy) == 0 {
		body := seriesBody(set, 0)
		body["start"] = from.Unix()
		body["end"] = to.Unix()
		writeJSON(w, http.StatusOK, body)
		return
	}
	out := map[string]any{}
	for n, series := range set.Series {
		out[series.Name] = seriesBody(events.SeriesSet{Buckets: set.Buckets, Series: []events.Series{series}}, n)
	}
	writeJSON(w, http.StatusOK, out)
}

// seriesBody renders one series in Sentry's timeseries shape: [unix, [{count: v}]].
func seriesBody(set events.SeriesSet, order int) map[string]any {
	points := make([]any, 0, len(set.Buckets))
	for n, at := range set.Buckets {
		var v float64
		if len(set.Series) > 0 && n < len(set.Series[0].Values) {
			v = set.Series[0].Values[n]
		}
		points = append(points, []any{at.Unix(), []any{map[string]any{"count": v}}})
	}
	return map[string]any{"data": points, "order": order, "isMetricsData": false}
}

/* ---- stats_v2 -------------------------------------------------------------- */

// handleSentryStatsV2 answers the outcome/quota endpoint. Every event we hold was
// accepted — we have no quota, no rate limiter and no spam filter to reject one — so
// the outcome dimension is a constant, and saying so is more useful than refusing
// the query the datasource's statsV2 type sends by default.
func (s *Server) handleSentryStatsV2(w http.ResponseWriter, r *http.Request) {
	if !s.sentryRead(w, r) {
		return
	}
	q := r.URL.Query()
	search, err := s.sentrySearch(r, "")
	if err != nil {
		sentryErr(w, http.StatusBadRequest, err.Error())
		return
	}
	from, to := sentryWindow(q)
	interval := sentryInterval(q.Get("interval"), to.Sub(from))

	// category is required by Sentry and by the datasource. We only ever hold error
	// events, so anything else is honestly zero rather than an error.
	categories := q["category"]
	zero := len(categories) > 0 && !contains(categories, "error") && !contains(categories, "default")
	// Likewise for outcome: filtering to anything but accepted selects nothing.
	if outcomes := q["outcome"]; len(outcomes) > 0 && !contains(outcomes, "accepted") {
		zero = true
	}

	var groupBy []string
	if contains(q["groupBy"], "project") {
		groupBy = []string{"project"}
	}
	set, err := s.events.EventSeries(r.Context(), events.StatsSearch{
		Search: search, From: from, To: to, Interval: interval,
		YAxis: "count()", GroupBy: groupBy, Top: 100,
	})
	if err != nil {
		s.log.Warn("sentry stats_v2 failed", "err", err)
		sentryErr(w, http.StatusBadRequest, err.Error())
		return
	}

	intervals := make([]string, 0, len(set.Buckets))
	for _, b := range set.Buckets {
		intervals = append(intervals, b.UTC().Format(time.RFC3339))
	}
	groups := make([]map[string]any, 0, len(set.Series))
	for _, series := range set.Series {
		by := map[string]string{"outcome": "accepted", "category": "error", "reason": ""}
		if p, ok := series.Group["project"]; ok {
			by["project"] = p
		}
		values := make([]int64, len(set.Buckets))
		var total int64
		for n, v := range series.Values {
			if n < len(values) && !zero {
				values[n] = int64(v)
				total += int64(v)
			}
		}
		groups = append(groups, map[string]any{
			"by": by,
			"totals": map[string]any{
				"sum(quantity)": total, "sum(times_seen)": total,
			},
			"series": map[string]any{
				"sum(quantity)": values, "sum(times_seen)": values,
			},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"start":     from.UTC().Format(time.RFC3339),
		"end":       to.UTC().Format(time.RFC3339),
		"intervals": intervals,
		"groups":    groups,
	})
}

/* ---- "Open in Sentry" links ------------------------------------------------ */

func (s *Server) handleSentryIssueLink(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	d, ok, err := s.events.Issue(r.Context(), id)
	if err != nil || !ok {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, s.baseURL+"/apps/"+d.Issue.Project+"/errors/"+strconv.FormatInt(id, 10), http.StatusFound)
}

// handleSentryEventLink resolves Grafana's discover link, whose last segment is
// "{project}:{event id}".
func (s *Server) handleSentryEventLink(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("ref")
	if n := strings.LastIndex(ref, ":"); n >= 0 {
		ref = ref[n+1:]
	}
	issueID, slug, ok, err := s.events.IssueOfEvent(r.Context(), ref)
	if err != nil || !ok {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, s.baseURL+"/apps/"+slug+"/errors/"+strconv.FormatInt(issueID, 10), http.StatusFound)
}

/* ---- query parsing --------------------------------------------------------- */

// sentryWindow reads Sentry's start/end pair. The datasource sends a zoneless
// timestamp for issues and events and RFC3339 for the stats endpoints, so both are
// accepted; a missing pair means the last 24 hours, as it does in Sentry.
func sentryWindow(q map[string][]string) (from, to time.Time) {
	get := func(k string) string {
		if v := q[k]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	now := time.Now().UTC()
	to = sentryTime(get("end"), now)
	from = sentryTime(get("start"), to.Add(-24*time.Hour))
	if statsPeriod := get("statsPeriod"); statsPeriod != "" && get("start") == "" {
		if d := sentryDuration(statsPeriod); d > 0 {
			from = to.Add(-d)
		}
	}
	return from, to
}

func sentryTime(v string, fallback time.Time) time.Time {
	v = strings.TrimSpace(v)
	if v == "" {
		return fallback
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t.UTC()
		}
	}
	// Unix seconds, which is what a hand-written dashboard variable usually holds.
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		return time.Unix(n, 0).UTC()
	}
	return fallback
}

// sentryDuration parses Sentry's compact spans: 30s, 5m, 2h, 14d, 1w.
func sentryDuration(v string) time.Duration {
	v = strings.TrimSpace(v)
	if len(v) < 2 {
		return 0
	}
	n, err := strconv.Atoi(v[:len(v)-1])
	if err != nil || n <= 0 {
		return 0
	}
	switch v[len(v)-1] {
	case 's':
		return time.Duration(n) * time.Second
	case 'm':
		return time.Duration(n) * time.Minute
	case 'h':
		return time.Duration(n) * time.Hour
	case 'd':
		return time.Duration(n) * 24 * time.Hour
	case 'w':
		return time.Duration(n) * 7 * 24 * time.Hour
	}
	return 0
}

// niceIntervals are the widths an automatic choice is allowed to land on. Every one
// divides a day, which keeps buckets on wall-clock boundaries — "6h" starts at 00:00,
// 06:00, 12:00, 18:00, where a raw span/100 would give something like 7h12m whose
// boundaries drift across the axis and read as nonsense on a date label.
var niceIntervals = []time.Duration{
	time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute,
	30 * time.Minute, time.Hour, 2 * time.Hour, 3 * time.Hour, 6 * time.Hour,
	12 * time.Hour, 24 * time.Hour, 7 * 24 * time.Hour,
}

// sentryInterval picks the bucket width: what was asked for, or the smallest nice
// width that keeps the window under about a hundred buckets.
func sentryInterval(v string, span time.Duration) time.Duration {
	if d := sentryDuration(v); d > 0 {
		return d
	}
	if span <= 0 {
		return time.Minute
	}
	want := span / 100
	for _, d := range niceIntervals {
		if d >= want {
			return d
		}
	}
	return niceIntervals[len(niceIntervals)-1]
}

// sentrySearch turns Sentry's search syntax into the subset we can answer, and folds
// in the project and environment parameters, which are sent separately.
//
// An unrecognised `key:value` becomes a tag filter rather than an error: ingest keeps
// every client tag, so the key space is open and `gpu:0` has to work. The cases that
// do fail are the ones where guessing would produce a *wrong* answer rather than an
// empty one — negation, boolean operators, and `is:` values we cannot evaluate.
func (s *Server) sentrySearch(r *http.Request, raw string) (events.Search, error) {
	out := events.Search{Tags: map[string]string{}}
	slugs := []string{}

	for _, tok := range splitQuoted(raw) {
		if strings.HasPrefix(tok, "!") || tok == "OR" || tok == "AND" || strings.HasPrefix(tok, "(") {
			return out, errUnsupportedQuery(tok)
		}
		key, value, isPair := strings.Cut(tok, ":")
		if !isPair || value == "" {
			out.Text = append(out.Text, strings.Trim(tok, `"`))
			continue
		}
		value = strings.Trim(value, `"`)
		switch key {
		case "is":
			switch value {
			case "unresolved", "resolved":
				out.Status = value
			case "ignored", "muted", "archived":
				out.Status = "archived"
			default:
				return out, errUnsupportedQuery(tok)
			}
		case "level":
			out.Level = value
		case "environment":
			out.Environments = append(out.Environments, value)
		case "project", "project.slug":
			slugs = append(slugs, value)
		case "error.type", "type":
			out.Kind = value
		case "message", "title":
			out.Text = append(out.Text, value)
		case "issue.id":
			// Not a tag: it is the row itself, and a wrong tag lookup here would
			// silently match nothing.
			if id, err := strconv.ParseInt(value, 10, 64); err == nil {
				out.IssueIDs = append(out.IssueIDs, id)
			}
		default:
			if k, ok := strings.CutPrefix(key, "tags["); ok {
				key = strings.TrimSuffix(k, "]")
			}
			out.Tags[key] = value
		}
	}

	q := r.URL.Query()
	out.Environments = append(out.Environments, nonEmpty(q["environment"])...)
	ids, refSlugs := splitProjectRefs(q["project"])
	out.ProjectIDs = ids
	slugs = append(slugs, refSlugs...)
	if len(slugs) > 0 {
		resolved, err := s.projectIDsForSlugs(r, slugs)
		if err != nil {
			return out, err
		}
		out.ProjectIDs = append(out.ProjectIDs, resolved...)
	}
	sort.Slice(out.ProjectIDs, func(a, b int) bool { return out.ProjectIDs[a] < out.ProjectIDs[b] })
	return out, nil
}

func errUnsupportedQuery(tok string) error {
	return &unsupportedQueryError{tok}
}

type unsupportedQueryError struct{ token string }

func (e *unsupportedQueryError) Error() string {
	return "unsupported search term " + strconv.Quote(e.token) +
		": bettersentryio accepts is:, level:, environment:, project:, error.type:, tag:value and bare words"
}

// splitProjectRefs separates numeric project ids from slugs. Sentry only sends ids,
// but -1 means "all projects" and a hand-written dashboard is likelier to hold a slug.
func splitProjectRefs(vals []string) (ids []int64, slugs []string) {
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v == "" || v == "-1" || v == "0" {
			continue
		}
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			ids = append(ids, n)
			continue
		}
		slugs = append(slugs, v)
	}
	return ids, slugs
}

func (s *Server) projectIDsForSlugs(r *http.Request, slugs []string) ([]int64, error) {
	projects, err := s.events.Projects(r.Context())
	if err != nil {
		return nil, err
	}
	byslug := make(map[string]int64, len(projects))
	for _, p := range projects {
		byslug[p.Slug] = p.ID
	}
	out := make([]int64, 0, len(slugs))
	for _, sl := range slugs {
		id, ok := byslug[sl]
		if !ok {
			return nil, &unknownProjectError{sl}
		}
		out = append(out, id)
	}
	return out, nil
}

type unknownProjectError struct{ slug string }

func (e *unknownProjectError) Error() string { return "unknown project " + strconv.Quote(e.slug) }

// splitQuoted splits on spaces but keeps "quoted phrases" whole, so
// `title:"connection refused"` is one term.
func splitQuoted(raw string) []string {
	var out []string
	var cur strings.Builder
	inQuote := false
	for _, r := range raw {
		switch {
		case r == '"':
			inQuote = !inQuote
			cur.WriteRune(r)
		case r == ' ' && !inQuote:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func nonEmpty(vals []string) []string {
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func contains(vals []string, want string) bool {
	for _, v := range vals {
		if v == want {
			return true
		}
	}
	return false
}

func atoiOr(v string, fallback int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
		return n
	}
	return fallback
}
