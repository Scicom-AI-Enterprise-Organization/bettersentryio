package events

import (
	"context"
	"encoding/json"
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
}

const issueColumns = `
	i.id, p.slug, p.name, i.fingerprint, i.environment, i.kind, i.culprit, i.title,
	i.level, i.times_seen, i.first_seen, i.last_seen, i.resolved_at`

// Issues lists a project's issues, newest sighting first. Unresolved only unless asked,
// because the list exists to answer "what is broken now".
func (s *Store) Issues(ctx context.Context, projectSlug string, includeResolved bool, limit int) ([]Issue, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `
		select`+issueColumns+`
		from issues i
		join projects p on p.id = i.project_id
		where p.slug = $1 and ($2 or i.resolved_at is null)
		order by i.last_seen desc
		limit $3`, projectSlug, includeResolved, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Issue{}
	for rows.Next() {
		var i Issue
		if err := rows.Scan(&i.ID, &i.Project, &i.ProjectName, &i.Fingerprint, &i.Environment,
			&i.Kind, &i.Culprit, &i.Title, &i.Level, &i.TimesSeen,
			&i.FirstSeen, &i.LastSeen, &i.ResolvedAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
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
		&d.Issue.ResolvedAt)
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
