package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Summary is the header row of the dashboard.
type Summary struct {
	Total, OK, Late, Missing, Stalled, Waiting int
	OpenIncidents                              int
}

// Unhealthy counts the states that need a human.
func (s Summary) Unhealthy() int { return s.Missing + s.Stalled }

// Row is one monitor as the UI shows it.
type Row struct {
	ID             int64
	Slug           string
	AppSlug        string
	AppName        string
	Environment    string
	Kind           string
	Status         string
	LastBeatAt     *time.Time
	LastProgress   *int64
	NextExpectedAt *time.Time
	EverySecs      int64
	GraceSecs      int64
	StallSecs      int64
	Muted          bool
	CreatedAt      time.Time
	OpenSince      *time.Time
	downtimeSecs   float64
	Beats24h       int64
	Spark          []Bucket
}

// Uptime is the share of the observed window with no open incident. A monitor
// younger than the window is measured against its own age, so a fresh monitor
// does not read as 0% or 100% on no evidence.
func (r Row) Uptime(window time.Duration) float64 {
	observed := window.Seconds()
	if age := time.Since(r.CreatedAt).Seconds(); age < observed {
		observed = age
	}
	if observed <= 0 {
		return 100
	}
	up := 100 * (1 - r.downtimeSecs/observed)
	switch {
	case up < 0:
		return 0
	case up > 100:
		return 100
	}
	return up
}

func (r Row) Uptime24h() float64 { return r.Uptime(24 * time.Hour) }

// Observed is the span the uptime figure actually covers. A monitor created ten
// minutes ago has no 24-hour history, and labelling its number "24h" without
// saying so invites the reader to misjudge it.
func (r Row) Observed(window time.Duration) time.Duration {
	if age := time.Since(r.CreatedAt); age < window {
		return age.Round(time.Second)
	}
	return window
}

func (r Row) Observed24h() time.Duration { return r.Observed(24 * time.Hour) }

// Bucket is one 5-minute slice of beat activity.
type Bucket struct {
	WindowStart   time.Time
	Beats         int
	ProgressDelta int64
}

// Incident is one row of the incident log.
type Incident struct {
	ID          int64
	Monitor     string
	Environment string
	Kind        string
	OpenedAt    time.Time
	ResolvedAt  *time.Time
	LastAlertAt *time.Time
	Delivered   int
}

func (i Incident) Open() bool { return i.ResolvedAt == nil }

func (i Incident) Duration() time.Duration {
	end := time.Now()
	if i.ResolvedAt != nil {
		end = *i.ResolvedAt
	}
	return end.Sub(i.OpenedAt).Round(time.Second)
}

const rowColumns = `
	m.id, m.slug,
	(select p.slug from projects p where p.id = m.project_id),
	(select p.name from projects p where p.id = m.project_id),
	coalesce(ms.environment, 'production'), m.kind,
	coalesce(ms.status, 'waiting'), ms.last_beat_at, ms.last_progress, ms.next_expected_at,
	coalesce((m.config->>'expected_every_secs')::bigint, 60),
	coalesce((m.config->>'grace_secs')::bigint, 0),
	coalesce((m.config->>'stall_window_secs')::bigint, 0),
	m.muted, m.created_at,
	(select i.opened_at from incidents i
	  where i.monitor_id = m.id and i.environment = coalesce(ms.environment, 'production')
	    and i.resolved_at is null limit 1),
	coalesce((select sum(extract(epoch from (
	            least(coalesce(i.resolved_at, now()), now())
	          - greatest(i.opened_at, now() - interval '24 hours'))))
	   from incidents i
	   where i.monitor_id = m.id and i.environment = coalesce(ms.environment, 'production')
	     and coalesce(i.resolved_at, now()) > now() - interval '24 hours'), 0),
	coalesce((select sum(br.beats) from beat_rollups br
	   where br.monitor_id = m.id and br.environment = coalesce(ms.environment, 'production')
	     and br.window_start > now() - interval '24 hours'), 0)`

func scanRow(rows pgx.Rows) (Row, error) {
	var r Row
	err := rows.Scan(&r.ID, &r.Slug, &r.AppSlug, &r.AppName, &r.Environment, &r.Kind, &r.Status,
		&r.LastBeatAt, &r.LastProgress, &r.NextExpectedAt,
		&r.EverySecs, &r.GraceSecs, &r.StallSecs, &r.Muted, &r.CreatedAt,
		&r.OpenSince, &r.downtimeSecs, &r.Beats24h)
	return r, err
}

func (e *Engine) Summary(ctx context.Context) (Summary, error) {
	var s Summary
	err := e.db.QueryRow(ctx, `
		select count(*),
		       count(*) filter (where coalesce(ms.status, 'waiting') = 'ok'),
		       count(*) filter (where ms.status = 'late'),
		       count(*) filter (where ms.status = 'missing'),
		       count(*) filter (where ms.status = 'stalled'),
		       count(*) filter (where coalesce(ms.status, 'waiting') = 'waiting'),
		       (select count(*) from incidents where resolved_at is null)
		from monitors m
		left join monitor_state ms on ms.monitor_id = m.id
		where not m.disabled`,
	).Scan(&s.Total, &s.OK, &s.Late, &s.Missing, &s.Stalled, &s.Waiting, &s.OpenIncidents)
	return s, err
}

// Rows returns every monitor, worst state first, each with a short activity
// sparkline. Two queries total regardless of monitor count.
func (e *Engine) Rows(ctx context.Context) ([]Row, error) {
	rows, err := e.db.Query(ctx, `
		select`+rowColumns+`
		from monitors m
		left join monitor_state ms on ms.monitor_id = m.id
		where not m.disabled
		order by (coalesce(ms.status, 'waiting') in ('missing', 'stalled')) desc,
		         (ms.status = 'late') desc, m.slug`)
	if err != nil {
		return nil, err
	}
	var out []Row
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	spark, err := e.sparklines(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	for i := range out {
		out[i].Spark = fillBuckets(spark[out[i].ID], time.Hour, now)
	}
	return out, nil
}

func (e *Engine) Row(ctx context.Context, slug string) (Row, error) {
	rows, err := e.db.Query(ctx, `
		select`+rowColumns+`
		from monitors m
		left join monitor_state ms on ms.monitor_id = m.id
		where m.slug = $1 and not m.disabled
		limit 1`, slug)
	if err != nil {
		return Row{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return Row{}, err
		}
		return Row{}, pgx.ErrNoRows
	}
	return scanRow(rows)
}

func (e *Engine) sparklines(ctx context.Context) (map[int64][]Bucket, error) {
	rows, err := e.db.Query(ctx, `
		select monitor_id, window_start, beats, progress_delta
		from beat_rollups
		where window_start > now() - interval '1 hour'
		order by monitor_id, window_start`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int64][]Bucket{}
	for rows.Next() {
		var (
			id int64
			b  Bucket
		)
		if err := rows.Scan(&id, &b.WindowStart, &b.Beats, &b.ProgressDelta); err != nil {
			return nil, err
		}
		out[id] = append(out[id], b)
	}
	return out, rows.Err()
}

// fillBuckets turns sparse rollup rows into a dense series over the whole window,
// inserting empty buckets where nothing was recorded. A gap has to be drawn to be
// read: an absent bar and a zero bar look identical, but only one is honest about
// a loop that stopped beating.
func fillBuckets(got []Bucket, window time.Duration, now time.Time) []Bucket {
	end := now.UTC().Truncate(rollupWindow)
	byStart := make(map[time.Time]Bucket, len(got))
	for _, b := range got {
		byStart[b.WindowStart.UTC().Truncate(rollupWindow)] = b
	}
	n := int(window / rollupWindow)
	out := make([]Bucket, 0, n+1)
	for i := n; i >= 0; i-- {
		start := end.Add(-time.Duration(i) * rollupWindow)
		if b, ok := byStart[start]; ok {
			out = append(out, b)
			continue
		}
		out = append(out, Bucket{WindowStart: start})
	}
	return out
}

// Activity is the beat history for one monitor's detail page.
func (e *Engine) Activity(ctx context.Context, monitorID int64, since time.Duration) ([]Bucket, error) {
	rows, err := e.db.Query(ctx, `
		select window_start, beats, progress_delta
		from beat_rollups
		where monitor_id = $1 and window_start > now() - make_interval(secs => $2::double precision)
		order by window_start`, monitorID, since.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Bucket
	for rows.Next() {
		var b Bucket
		if err := rows.Scan(&b.WindowStart, &b.Beats, &b.ProgressDelta); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return fillBuckets(out, since, time.Now()), nil
}

// Incidents lists the incident log, newest first. A zero monitorID means all.
func (e *Engine) Incidents(ctx context.Context, monitorID int64, limit int) ([]Incident, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := e.db.Query(ctx, `
		select i.id, m.slug, i.environment, i.kind, i.opened_at, i.resolved_at, i.last_alert_at,
		       (select count(*) from notifications n
		         where n.dedup_key = 'incident:' || i.id || ':open')
		from incidents i
		join monitors m on m.id = i.monitor_id
		where ($1 = 0 or i.monitor_id = $1)
		order by (i.resolved_at is null) desc, i.opened_at desc
		limit $2`, monitorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Incident
	for rows.Next() {
		var i Incident
		if err := rows.Scan(&i.ID, &i.Monitor, &i.Environment, &i.Kind,
			&i.OpenedAt, &i.ResolvedAt, &i.LastAlertAt, &i.Delivered); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (e *Engine) SetMuted(ctx context.Context, slug string, muted bool) error {
	tag, err := e.db.Exec(ctx, `update monitors set muted = $2 where slug = $1`, slug, muted)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// Config returns the monitor's stored configuration for display.
func (e *Engine) Config(ctx context.Context, slug string) (map[string]any, error) {
	var raw []byte
	if err := e.db.QueryRow(ctx, `select config from monitors where slug = $1`, slug).Scan(&raw); err != nil {
		return nil, err
	}
	cfg := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

// Project and Channel back the settings page.
type Project struct {
	Slug      string
	Name      string
	CreatedAt time.Time
	Keys      []Key
}

type Key struct {
	PublicKey string
	CreatedAt time.Time
	Revoked   bool
}

type Channel struct {
	ID      int64
	Name    string
	Type    string
	Target  string
	Enabled bool
}

func (e *Engine) Projects(ctx context.Context) ([]Project, error) {
	rows, err := e.db.Query(ctx, `
		select p.slug, p.name, p.created_at, k.public_key, k.created_at, (k.revoked_at is not null)
		from projects p
		left join ingest_keys k on k.project_id = p.id
		order by p.slug, k.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var (
		out  []Project
		seen = map[string]int{}
	)
	for rows.Next() {
		var (
			slug, name string
			created    time.Time
			key        *string
			keyCreated *time.Time
			revoked    *bool
		)
		if err := rows.Scan(&slug, &name, &created, &key, &keyCreated, &revoked); err != nil {
			return nil, err
		}
		idx, ok := seen[slug]
		if !ok {
			out = append(out, Project{Slug: slug, Name: name, CreatedAt: created})
			idx = len(out) - 1
			seen[slug] = idx
		}
		if key != nil {
			k := Key{PublicKey: *key}
			if keyCreated != nil {
				k.CreatedAt = *keyCreated
			}
			if revoked != nil {
				k.Revoked = *revoked
			}
			out[idx].Keys = append(out[idx].Keys, k)
		}
	}
	return out, rows.Err()
}

func (e *Engine) Channels(ctx context.Context) ([]Channel, error) {
	rows, err := e.db.Query(ctx, `select id, name, type, config, enabled from channels order by name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Channel
	for rows.Next() {
		var (
			c   Channel
			raw []byte
		)
		if err := rows.Scan(&c.ID, &c.Name, &c.Type, &raw, &c.Enabled); err != nil {
			return nil, err
		}
		cfg := map[string]string{}
		_ = json.Unmarshal(raw, &cfg)
		// Never render a secret. A webhook URL is itself a credential, and a bot
		// token certainly is, so both are reduced to something recognisable.
		switch {
		case cfg["url"] != "":
			c.Target = redactURL(cfg["url"])
		case cfg["bot_token"] != "":
			c.Target = "chat " + cfg["chat_id"]
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func redactURL(raw string) string {
	// Keep scheme+host so it is identifiable; drop the path, which is the secret
	// part of every chat webhook.
	for i := 0; i < len(raw); i++ {
		if raw[i] == '/' && i > 8 && raw[i-1] != '/' {
			return raw[:i] + "/…"
		}
	}
	return raw
}

var errNoRows = errors.New("no rows")
