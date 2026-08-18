package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Issue is a row in the issue list.
type Issue struct {
	ID          int64      `json:"id"`
	Project     string     `json:"project"`
	ProjectName string     `json:"project_name"`
	Fingerprint string     `json:"fingerprint"`
	Environment string     `json:"environment"`
	Kind        string     `json:"kind"`
	Culprit     string     `json:"culprit"`
	Title       string     `json:"title"`
	Level       string     `json:"level"`
	TimesSeen   int64      `json:"times_seen"`
	FirstSeen   time.Time  `json:"first_seen"`
	LastSeen    time.Time  `json:"last_seen"`
	ResolvedAt  *time.Time `json:"resolved_at"`
	ArchivedAt  *time.Time `json:"archived_at"`
	// ArchivedUntil: null while archived = forever; a past value = expired.
	ArchivedUntil *time.Time `json:"archived_until"`
	ArchiveRecur  bool       `json:"archive_recur"`
	Priority      string     `json:"priority"`
	// Tags: the client's tags merged with server-derived ones (level,
	// environment, release, transaction, url, mechanism, handled, ...).
	Tags map[string]string `json:"tags"`
	// Activity is the last 24 hours of events, one bucket per hour, oldest
	// first — the issue list's trend sparkline. Filled by Issues(), not by the
	// detail lookup.
	Activity []TrendBucket `json:"activity,omitempty"`
}

// TrendBucket is one hour of an issue's event volume.
type TrendBucket struct {
	At    time.Time `json:"at"`
	Count int64     `json:"count"`
}

const issueColumns = `
	i.id, p.slug, p.name, i.fingerprint, i.environment, i.kind, i.culprit, i.title,
	i.level, i.times_seen, i.first_seen, i.last_seen, i.resolved_at,
	i.archived_at, i.archived_until, i.archive_recur, i.priority, i.tags`

// Issues lists a project's issues, newest sighting first. Unresolved only unless asked,
// because the list exists to answer "what is broken now".
func (s *Store) Issues(ctx context.Context, projectSlug string, includeResolved, includeArchived bool, limit int, tagFilters map[string]string) ([]Issue, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := `
		select` + issueColumns + `
		from issues i
		join projects p on p.id = i.project_id
		where p.slug = $1 and ($2 or i.resolved_at is null)
		  and ($3 or i.archived_at is null
		       or (i.archived_until is not null and i.archived_until < now()))`
	args := []any{projectSlug, includeResolved, includeArchived}
	for k, v := range tagFilters {
		query += fmt.Sprintf(" and i.tags->>$%d = $%d", len(args)+1, len(args)+2)
		args = append(args, k, v)
	}
	query += fmt.Sprintf(" order by i.last_seen desc limit $%d", len(args)+1)
	args = append(args, limit)

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Issue{}
	for rows.Next() {
		var i Issue
		if err := rows.Scan(&i.ID, &i.Project, &i.ProjectName, &i.Fingerprint, &i.Environment,
			&i.Kind, &i.Culprit, &i.Title, &i.Level, &i.TimesSeen,
			&i.FirstSeen, &i.LastSeen, &i.ResolvedAt,
			&i.ArchivedAt, &i.ArchivedUntil, &i.ArchiveRecur, &i.Priority, &i.Tags); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.fillActivity(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// fillActivity attaches the 24-hour trend to each issue: one grouped query for
// the whole page, never one per row.
func (s *Store) fillActivity(ctx context.Context, issues []Issue) error {
	if len(issues) == 0 {
		return nil
	}
	ids := make([]int64, len(issues))
	for n, i := range issues {
		ids[n] = i.ID
	}
	rows, err := s.db.Query(ctx, `
		select issue_id, date_trunc('hour', received_at), count(*)
		from events
		where issue_id = any($1) and received_at > now() - interval '24 hours'
		group by 1, 2`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()

	counts := map[int64]map[time.Time]int64{}
	for rows.Next() {
		var id int64
		var at time.Time
		var n int64
		if err := rows.Scan(&id, &at, &n); err != nil {
			return err
		}
		if counts[id] == nil {
			counts[id] = map[time.Time]int64{}
		}
		counts[id][at.UTC()] = n
	}
	if err := rows.Err(); err != nil {
		return err
	}

	newest := time.Now().UTC().Truncate(time.Hour)
	for n := range issues {
		buckets := make([]TrendBucket, 24)
		for h := 0; h < 24; h++ {
			at := newest.Add(time.Duration(h-23) * time.Hour)
			buckets[h] = TrendBucket{At: at, Count: counts[issues[n].ID][at]}
		}
		issues[n].Activity = buckets
	}
	return nil
}

// Counts is the header for a project's issue list.
type Counts struct {
	Unresolved int64 `json:"unresolved"`
	Events     int64 `json:"events"`
}

func (s *Store) Counts(ctx context.Context, projectSlug string) (Counts, error) {
	var c Counts
	err := s.db.QueryRow(ctx, `
		select count(*) filter (where i.resolved_at is null),
		       coalesce(sum(i.times_seen), 0)
		from issues i join projects p on p.id = i.project_id
		where p.slug = $1`, projectSlug).Scan(&c.Unresolved, &c.Events)
	return c, err
}

// Detail is one issue plus its most recent event, which is what a stacktrace view needs.
type Detail struct {
	Issue  Issue           `json:"issue"`
	Latest json.RawMessage `json:"latest_event"`
	Recent []Occurrence    `json:"recent"`
}

type Occurrence struct {
	ID         int64     `json:"id"`
	ReceivedAt time.Time `json:"received_at"`
	Message    string    `json:"message"`
}

func (s *Store) Issue(ctx context.Context, id int64) (Detail, bool, error) {
	var d Detail
	err := s.db.QueryRow(ctx, `
		select`+issueColumns+`
		from issues i join projects p on p.id = i.project_id
		where i.id = $1`, id).Scan(
		&d.Issue.ID, &d.Issue.Project, &d.Issue.ProjectName, &d.Issue.Fingerprint,
		&d.Issue.Environment, &d.Issue.Kind, &d.Issue.Culprit, &d.Issue.Title,
		&d.Issue.Level, &d.Issue.TimesSeen, &d.Issue.FirstSeen, &d.Issue.LastSeen,
		&d.Issue.ResolvedAt, &d.Issue.ArchivedAt, &d.Issue.ArchivedUntil,
		&d.Issue.ArchiveRecur, &d.Issue.Priority, &d.Issue.Tags)
	if err != nil {
		return d, false, nil //nolint:nilerr // absent is not an error to the caller
	}

	// The newest event carries the stacktrace we render.
	var payload []byte
	if err := s.db.QueryRow(ctx,
		`select payload from events where issue_id = $1 order by received_at desc limit 1`, id,
	).Scan(&payload); err == nil {
		d.Latest = payload
	}

	rows, err := s.db.Query(ctx,
		`select id, received_at, message from events where issue_id = $1
		 order by received_at desc limit 25`, id)
	if err != nil {
		return d, true, err
	}
	defer rows.Close()
	d.Recent = []Occurrence{}
	for rows.Next() {
		var o Occurrence
		if err := rows.Scan(&o.ID, &o.ReceivedAt, &o.Message); err != nil {
			return d, true, err
		}
		d.Recent = append(d.Recent, o)
	}
	return d, true, rows.Err()
}

// SetResolved marks an issue fixed. A later occurrence reopens it automatically — see
// the upsert in Ingest — so this is a claim, not a suppression.
func (s *Store) SetResolved(ctx context.Context, id int64, resolved bool) error {
	_, err := s.db.Exec(ctx,
		`update issues set resolved_at = case when $2 then now() else null end where id = $1`,
		id, resolved)
	return err
}

// SetArchived archives an issue three ways — forever (until nil, recur false),
// for a duration (until set), or until it occurs again (recur true; ingest
// clears it on the next event). Un-archiving passes archived=false.
func (s *Store) SetArchived(ctx context.Context, id int64, archived bool, until *time.Time, recur bool) error {
	if !archived {
		_, err := s.db.Exec(ctx,
			`update issues set archived_at = null, archived_until = null, archive_recur = false where id = $1`, id)
		return err
	}
	_, err := s.db.Exec(ctx, `
		update issues set archived_at = now(), archived_until = $2, archive_recur = $3 where id = $1`,
		id, until, recur)
	return err
}

// SetPriority stamps the triage priority ('' clears it).
func (s *Store) SetPriority(ctx context.Context, id int64, priority string) error {
	_, err := s.db.Exec(ctx, `update issues set priority = $2 where id = $1`, id, priority)
	return err
}

// DeleteIssue removes an issue and its events (cascade).
func (s *Store) DeleteIssue(ctx context.Context, id int64) (bool, error) {
	tag, err := s.db.Exec(ctx, `delete from issues where id = $1`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// EventByID returns one stored event of an issue, for prev/next browsing.
func (s *Store) EventByID(ctx context.Context, issueID, eventID int64) (json.RawMessage, bool, error) {
	var payload []byte
	err := s.db.QueryRow(ctx,
		`select payload from events where id = $1 and issue_id = $2`, eventID, issueID,
	).Scan(&payload)
	if err != nil {
		return nil, false, nil //nolint:nilerr // absent is not an error to the caller
	}
	return payload, true, nil
}
