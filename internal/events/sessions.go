package events

// Release health from Sentry session items. Two wire shapes exist and the
// Python SDK picks per mode: long-running scripts send individual "session"
// updates; request-mode servers (ASGI/WSGI) pre-bucket into a "sessions"
// aggregate item. Both reduce to the same hourly counters here.

import (
	"context"
	"encoding/json"
	"time"
)

type sessionAttrs struct {
	Release     string `json:"release"`
	Environment string `json:"environment"`
}

// SessionCounts is one hourly increment derived from either item shape.
type SessionCounts struct {
	Release     string
	Environment string
	Hour        time.Time
	Exited      int64
	Errored     int64
	Crashed     int64
	Abnormal    int64
}

func (c SessionCounts) total() int64 { return c.Exited + c.Errored + c.Crashed + c.Abnormal }

// ParseSessionItem reduces a single-session update to counts. Only terminal
// updates count — an ongoing "ok" heartbeat would double-book the session when
// its exit arrives. nil means nothing to record, which is not an error.
func ParseSessionItem(payload []byte, now time.Time) *SessionCounts {
	var u struct {
		Status    string       `json:"status"`
		Errors    int64        `json:"errors"`
		Timestamp *FlexTime    `json:"timestamp"`
		Started   *FlexTime    `json:"started"`
		Attrs     sessionAttrs `json:"attrs"`
	}
	if err := json.Unmarshal(payload, &u); err != nil {
		return nil
	}
	c := SessionCounts{
		Release:     u.Attrs.Release,
		Environment: firstNonEmpty(u.Attrs.Environment, "production"),
		Hour:        sessionHour(u.Timestamp, u.Started, now),
	}
	switch u.Status {
	case "exited":
		if u.Errors > 0 {
			c.Errored = 1
		} else {
			c.Exited = 1
		}
	case "crashed":
		c.Crashed = 1
	case "abnormal":
		c.Abnormal = 1
	default: // "ok" and anything future: an update, not an ending
		return nil
	}
	return &c
}

// ParseSessionsItem reduces a request-mode aggregate item to counts, one entry
// per pre-bucketed hour the SDK reported.
func ParseSessionsItem(payload []byte, now time.Time) []SessionCounts {
	var agg struct {
		Aggregates []struct {
			Started  *FlexTime `json:"started"`
			Exited   int64     `json:"exited"`
			Errored  int64     `json:"errored"`
			Crashed  int64     `json:"crashed"`
			Abnormal int64     `json:"abnormal"`
		} `json:"aggregates"`
		Attrs sessionAttrs `json:"attrs"`
	}
	if err := json.Unmarshal(payload, &agg); err != nil {
		return nil
	}
	out := make([]SessionCounts, 0, len(agg.Aggregates))
	for _, a := range agg.Aggregates {
		c := SessionCounts{
			Release:     agg.Attrs.Release,
			Environment: firstNonEmpty(agg.Attrs.Environment, "production"),
			Hour:        sessionHour(a.Started, nil, now),
			Exited:      a.Exited,
			Errored:     a.Errored,
			Crashed:     a.Crashed,
			Abnormal:    a.Abnormal,
		}
		if c.total() > 0 {
			out = append(out, c)
		}
	}
	return out
}

func sessionHour(a, b *FlexTime, now time.Time) time.Time {
	t := now
	if a != nil && !a.IsZero() {
		t = a.Time
	} else if b != nil && !b.IsZero() {
		t = b.Time
	}
	// Same clamp as event timestamps: a skewed clock must not write history.
	if t.After(now.Add(time.Minute)) || t.Before(now.Add(-30*24*time.Hour)) {
		t = now
	}
	return t.UTC().Truncate(time.Hour)
}

// RecordSessions folds one increment into the hourly rollup.
func (s *Store) RecordSessions(ctx context.Context, projectID int64, c SessionCounts) error {
	if c.total() == 0 {
		return nil
	}
	_, err := s.db.Exec(ctx, `
		insert into release_health (project_id, release, environment, hour, exited, errored, crashed, abnormal)
		values ($1, $2, $3, $4, $5, $6, $7, $8)
		on conflict (project_id, release, environment, hour) do update set
			exited   = release_health.exited   + excluded.exited,
			errored  = release_health.errored  + excluded.errored,
			crashed  = release_health.crashed  + excluded.crashed,
			abnormal = release_health.abnormal + excluded.abnormal`,
		projectID, truncate(c.Release, 200), truncate(c.Environment, 64), c.Hour,
		c.Exited, c.Errored, c.Crashed, c.Abnormal)
	return err
}

// ReleaseRow is one release+environment in the releases view.
type ReleaseRow struct {
	Release     string    `json:"release"`
	Environment string    `json:"environment"`
	Sessions    int64     `json:"sessions"`
	Exited      int64     `json:"exited"`
	Errored     int64     `json:"errored"`
	Crashed     int64     `json:"crashed"`
	Abnormal    int64     `json:"abnormal"`
	CrashFree   float64   `json:"crash_free"`
	FirstSeen   time.Time `json:"first_seen"`
	LastSeen    time.Time `json:"last_seen"`
}

// ReleaseHealth lists a project's releases by session health since `since`,
// most recently seen first.
func (s *Store) ReleaseHealth(ctx context.Context, projectSlug string, since time.Time) ([]ReleaseRow, error) {
	rows, err := s.db.Query(ctx, `
		select rh.release, rh.environment,
		       sum(rh.exited), sum(rh.errored), sum(rh.crashed), sum(rh.abnormal),
		       min(rh.hour), max(rh.hour)
		from release_health rh
		join projects p on p.id = rh.project_id
		where p.slug = $1 and rh.hour >= $2
		group by rh.release, rh.environment
		order by max(rh.hour) desc, rh.release desc`,
		projectSlug, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ReleaseRow{}
	for rows.Next() {
		var r ReleaseRow
		if err := rows.Scan(&r.Release, &r.Environment,
			&r.Exited, &r.Errored, &r.Crashed, &r.Abnormal,
			&r.FirstSeen, &r.LastSeen); err != nil {
			return nil, err
		}
		r.Sessions = r.Exited + r.Errored + r.Crashed + r.Abnormal
		r.CrashFree = 100
		if r.Sessions > 0 {
			r.CrashFree = 100 * (1 - float64(r.Crashed)/float64(r.Sessions))
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
