package monitor

import (
	"context"
	"time"
)

// App is a service that reports to us — one row on the Apps page. Monitors are
// created inside an app by their first beat, so an app with zero monitors is one
// that has been registered but has not reported yet.
type App struct {
	Slug         string
	Name         string
	Platform     string
	CreatedAt    time.Time
	Key          string
	Monitors     int
	Unhealthy    int
	LastBeatAt   *time.Time
	OpenIncident bool
}

// Connected reports whether anything has ever arrived from this app.
func (a App) Connected() bool { return a.Monitors > 0 }

func (e *Engine) Apps(ctx context.Context) ([]App, error) {
	rows, err := e.db.Query(ctx, `
		select p.slug, p.name, p.platform, p.created_at,
		       coalesce((select k.public_key from ingest_keys k
		                  where k.project_id = p.id and k.revoked_at is null
		                  order by k.created_at limit 1), ''),
		       (select count(*) from monitors m where m.project_id = p.id and not m.disabled),
		       (select count(*) from monitors m
		          join monitor_state ms on ms.monitor_id = m.id
		         where m.project_id = p.id and not m.disabled
		           and ms.status in ('missing', 'stalled')),
		       (select max(ms.last_beat_at) from monitors m
		          join monitor_state ms on ms.monitor_id = m.id
		         where m.project_id = p.id),
		       exists (select 1 from monitors m
		                 join incidents i on i.monitor_id = m.id
		                where m.project_id = p.id and i.resolved_at is null)
		from projects p
		order by p.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []App
	for rows.Next() {
		var a App
		if err := rows.Scan(&a.Slug, &a.Name, &a.Platform, &a.CreatedAt, &a.Key,
			&a.Monitors, &a.Unhealthy, &a.LastBeatAt, &a.OpenIncident); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// App returns one app, or ok=false if the slug is unknown.
func (e *Engine) App(ctx context.Context, slug string) (App, bool, error) {
	apps, err := e.Apps(ctx)
	if err != nil {
		return App{}, false, err
	}
	for _, a := range apps {
		if a.Slug == slug {
			return a, true, nil
		}
	}
	return App{}, false, nil
}

// RowsForApp lists the monitors belonging to one app.
func (e *Engine) RowsForApp(ctx context.Context, slug string) ([]Row, error) {
	rows, err := e.db.Query(ctx, `
		select`+rowColumns+`
		from monitors m
		join projects p on p.id = m.project_id
		left join monitor_state ms on ms.monitor_id = m.id
		where p.slug = $1 and not m.disabled
		order by (coalesce(ms.status, 'waiting') in ('missing', 'stalled')) desc, m.slug`, slug)
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
