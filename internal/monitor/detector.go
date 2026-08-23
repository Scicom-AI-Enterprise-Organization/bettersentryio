package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/alert"
	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/store"
)

// Detector is the whole absence-detection mechanism: one ticker, three indexed
// sweeps. Sentry needs a Kafka-derived distributed clock for the same job because
// its consumers span machines; a single node does not need consensus about "now".
type Detector struct {
	db         *store.DB
	alerter    *alert.Alerter
	log        *slog.Logger
	interval   time.Duration
	baseURL    string
	alertRetry time.Duration

	lastTickUnix atomic.Int64
	ticks        atomic.Int64
	failures     atomic.Int64 // consecutive failed sweeps
	lastErr      atomic.Pointer[string]

	// leading: whether this replica holds LockDetector and therefore sweeps. The
	// lock handle itself is only touched from Run's goroutine; the flag is atomic
	// because /-/health and the metrics endpoint read it from request goroutines.
	leading atomic.Bool
	lock    *store.AdvisoryLock
}

func NewDetector(db *store.DB, a *alert.Alerter, log *slog.Logger, interval time.Duration, baseURL string) *Detector {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	return &Detector{
		db: db, alerter: a, log: log, interval: interval, baseURL: baseURL,
		alertRetry: defaultAlertRetry,
	}
}

func (d *Detector) Interval() time.Duration { return d.interval }
func (d *Detector) Ticks() int64            { return d.ticks.Load() }

// Failures and LastError expose a detector that is running but failing. Without
// these, a sweep erroring on every tick looks identical to a healthy idle system
// from the outside — the precise blind spot this project exists to remove.
func (d *Detector) Failures() int64 { return d.failures.Load() }

func (d *Detector) LastError() string {
	if p := d.lastErr.Load(); p != nil {
		return *p
	}
	return ""
}

// Leading reports whether this replica holds the detector lock. A standby replica
// with an old tick age is healthy — /-/health and the alert rules must read this
// before treating staleness as a problem, or a two-replica install pages on the
// replica that is correctly doing nothing.
func (d *Detector) Leading() bool { return d.leading.Load() }

// LastTickAge reports how long ago the detector completed a sweep. This is the
// number /-/health exists to publish: if this grows, we are blind, and we say so
// instead of returning a cheerful 200 like the health check that cost us two days.
func (d *Detector) LastTickAge() time.Duration {
	ns := d.lastTickUnix.Load()
	if ns == 0 {
		return 0
	}
	return time.Since(time.Unix(0, ns))
}

func (d *Detector) Run(ctx context.Context) {
	t := time.NewTicker(d.interval)
	defer t.Stop()
	defer func() {
		// Releasing on the way out lets a standby take over within one tick of a
		// graceful shutdown instead of waiting for the connection to be reaped.
		if d.lock != nil {
			d.lock.Release()
			d.lock = nil
		}
		d.leading.Store(false)
	}()
	d.log.Info("detector started", "interval", d.interval)

	// Whichever replica holds the advisory lock detects; the rest serve ingest and
	// UI, and say so (ARCHITECTURE, "one detector per database"). The lock is held
	// across ticks on its dedicated connection rather than re-raced every tick —
	// cheaper, and failover falls out of the semantics: a session lock dies with its
	// connection, so the holder crashing IS the standby's signal to take over.
	standbyLogged := false
	for {
		select {
		case <-ctx.Done():
			d.log.Info("detector stopped")
			return
		case <-t.C:
			// Held locks can be lost silently — a database restart drops the
			// connection and the lock with it, while this process keeps running.
			// The per-tick ping turns that into an explicit demotion instead of
			// two replicas both believing they lead.
			if d.lock != nil && !d.lock.Alive(ctx) {
				d.lock.Release()
				d.lock = nil
				d.leading.Store(false)
				d.log.Warn("detector lock lost — standing by until re-acquired")
			}
			if d.lock == nil {
				lock, err := d.db.TryAdvisoryLock(ctx, store.LockDetector)
				if err != nil {
					if ctx.Err() == nil {
						msg := err.Error()
						d.failures.Add(1)
						d.lastErr.Store(&msg)
						d.log.Error("detector lock attempt failed", "err", err)
					}
					continue
				}
				if lock == nil {
					if !standbyLogged {
						d.log.Info("detector standing by — another replica holds the lock")
						standbyLogged = true
					}
					continue
				}
				d.lock = lock
				d.leading.Store(true)
				standbyLogged = false
				d.log.Info("detector lock acquired — this replica detects")
			}
			if stats, err := d.Tick(ctx, time.Now().UTC()); err != nil {
				if ctx.Err() == nil {
					msg := err.Error()
					d.failures.Add(1)
					d.lastErr.Store(&msg)
					d.log.Error("detector tick failed", "err", err, "consecutive", d.failures.Load())
				}
			} else if stats.any() {
				d.log.Info("detector tick",
					"late", stats.Late, "missing", stats.Missing,
					"stalled", stats.Stalled, "alerted", stats.Alerted)
			}
		}
	}
}

type Stats struct {
	Late, Missing, Stalled, Alerted int
	Reanchored                      int
}

func (s Stats) any() bool {
	return s.Late+s.Missing+s.Stalled+s.Alerted+s.Reanchored > 0
}

func (d *Detector) Tick(ctx context.Context, now time.Time) (Stats, error) {
	var stats Stats

	// A large gap means the host slept or the process was paused, not that every
	// loop died. Re-anchor the schedules and skip this round's alerting rather
	// than firing a backlog of false alarms.
	if prev := d.lastTickUnix.Load(); prev != 0 {
		if gap := now.Sub(time.Unix(0, prev)); gap > 5*d.interval {
			n, err := d.reanchor(ctx, now)
			if err != nil {
				return stats, err
			}
			stats.Reanchored = n
			d.lastTickUnix.Store(now.UnixNano())
			d.ticks.Add(1)
			d.log.Warn("clock gap detected — re-anchored schedules, skipped alerting",
				"gap", gap.Round(time.Second), "monitors", n)
			return stats, nil
		}
	}

	tag, err := d.db.Exec(ctx, `
		update monitor_state set status = 'late', updated_at = now()
		where status = 'ok' and next_expected_at < $1`, now)
	if err != nil {
		return stats, fmt.Errorf("late sweep: %w", err)
	}
	stats.Late = int(tag.RowsAffected())

	if stats.Missing, err = d.sweepMissing(ctx, now); err != nil {
		return stats, err
	}
	// Ordered after the miss sweep on purpose: a loop whose beats have stopped is
	// MISSING, not STALLED, even though its progress counter is also frozen.
	if stats.Stalled, err = d.sweepStalled(ctx, now); err != nil {
		return stats, err
	}
	if stats.Alerted, err = d.alertOpenIncidents(ctx); err != nil {
		return stats, err
	}

	d.lastTickUnix.Store(now.UnixNano())
	d.ticks.Add(1)
	d.failures.Store(0)
	d.lastErr.Store(nil)
	return stats, nil
}

func (d *Detector) reanchor(ctx context.Context, now time.Time) (int, error) {
	// $1 is cast explicitly: without it Postgres infers the parameter type from
	// `$1 + make_interval(...)` and can resolve it to interval instead of timestamptz.
	tag, err := d.db.Exec(ctx, `
		update monitor_state ms set
			next_expected_at = $1::timestamptz + make_interval(secs => coalesce((m.config->>'expected_every_secs')::double precision, 60)),
			next_expected_latest = $1::timestamptz + make_interval(secs =>
				coalesce((m.config->>'expected_every_secs')::double precision, 60)
				+ coalesce((m.config->>'grace_secs')::double precision, 60)),
			window_started_at = case when ms.window_started_at is null then null else $1::timestamptz end,
			updated_at = now()
		from monitors m
		where ms.monitor_id = m.id and ms.status in ('ok', 'late')`, now)
	if err != nil {
		return 0, fmt.Errorf("re-anchor: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func (d *Detector) sweepMissing(ctx context.Context, now time.Time) (int, error) {
	// 'stalled' is included so a stalled loop that then stops beating altogether is
	// relabelled MISSING, which is the truer description. Its incident is already
	// open and announced, so this reclassification does not re-alert.
	rows, err := d.db.Query(ctx, `
		update monitor_state ms set status = 'missing', updated_at = now()
		from monitors m
		where ms.monitor_id = m.id
		  and ms.status in ('ok', 'late', 'stalled')
		  and ms.next_expected_latest < $1
		  and not m.disabled
		returning m.id, m.slug, ms.environment, ms.last_beat_at,
		          coalesce((m.config->>'expected_every_secs')::bigint, 60)`, now)
	if err != nil {
		return 0, fmt.Errorf("missing sweep: %w", err)
	}
	type hit struct {
		id       int64
		slug     string
		env      string
		lastBeat *time.Time
		every    int64
	}
	var hits []hit
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.id, &h.slug, &h.env, &h.lastBeat, &h.every); err != nil {
			rows.Close()
			return 0, err
		}
		hits = append(hits, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for _, h := range hits {
		if err := openIncident(ctx, d.db, h.id, h.env, "missing", now); err != nil {
			return len(hits), err
		}
		silence := "unknown"
		if h.lastBeat != nil {
			silence = now.Sub(*h.lastBeat).Round(time.Second).String()
		}
		d.log.Warn("monitor MISSING", "monitor", h.slug, "env", h.env,
			"silent_for", silence, "expected_every_s", h.every)
	}
	return len(hits), nil
}

func (d *Detector) sweepStalled(ctx context.Context, now time.Time) (int, error) {
	rows, err := d.db.Query(ctx, `
		update monitor_state ms set status = 'stalled', updated_at = now()
		from monitors m
		where ms.monitor_id = m.id
		  and ms.status in ('ok', 'late')
		  and not m.disabled
		  and m.kind = 'loop'
		  and ms.window_started_at is not null
		  and coalesce((m.config->>'stall_window_secs')::bigint, 0) > 0
		  and ms.window_started_at < $1::timestamptz - make_interval(secs => (m.config->>'stall_window_secs')::double precision)
		returning m.id, m.slug, ms.environment, ms.last_progress, ms.window_started_at`, now)
	if err != nil {
		return 0, fmt.Errorf("stall sweep: %w", err)
	}
	type hit struct {
		id     int64
		slug   string
		env    string
		prog   *int64
		frozen time.Time
	}
	var hits []hit
	for rows.Next() {
		var h hit
		if err := rows.Scan(&h.id, &h.slug, &h.env, &h.prog, &h.frozen); err != nil {
			rows.Close()
			return 0, err
		}
		hits = append(hits, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for _, h := range hits {
		if err := openIncident(ctx, d.db, h.id, h.env, "stalled", now); err != nil {
			return len(hits), err
		}
		d.log.Warn("monitor STALLED — beating but making no progress",
			"monitor", h.slug, "env", h.env,
			"frozen_for", now.Sub(h.frozen).Round(time.Second), "progress", derefInt(h.prog))
	}
	return len(hits), nil
}

func openIncident(ctx context.Context, db *store.DB, monitorID int64, env, kind string, now time.Time) error {
	// The partial unique index keeps this to one open incident per monitor
	// environment, so a flapping monitor cannot pile up duplicates.
	_, err := db.Exec(ctx, `
		insert into incidents (monitor_id, environment, kind, opened_at)
		values ($1, $2, $3, $4)
		on conflict (monitor_id, environment) where resolved_at is null do nothing`,
		monitorID, env, kind, now)
	return err
}

// defaultAlertRetry bounds how often a still-undelivered incident is re-attempted.
const defaultAlertRetry = time.Minute

// SetAlertRetry overrides the retry interval. Tests use it to avoid waiting a
// minute for the retry path.
func (d *Detector) SetAlertRetry(v time.Duration) { d.alertRetry = v }

// alertOpenIncidents publishes open incidents that no enabled channel has received
// yet, and keeps retrying until they land. The notifications ledger — not a flag on
// the incident — is the record of what was delivered, so a chat outage delays an
// alert instead of losing it. Opening an incident and announcing it stay separate
// steps, so dying in between costs nothing either.
func (d *Detector) alertOpenIncidents(ctx context.Context) (int, error) {
	rows, err := d.db.Query(ctx, `
		select i.id, i.kind, i.opened_at, m.slug, m.project_id, i.environment,
		       ms.last_beat_at, ms.last_progress, ms.window_started_at,
		       coalesce((m.config->>'expected_every_secs')::bigint, 60),
		       coalesce((m.config->>'stall_window_secs')::bigint, 0)
		from incidents i
		join monitors m on m.id = i.monitor_id
		left join monitor_state ms
		       on ms.monitor_id = i.monitor_id and ms.environment = i.environment
		where i.resolved_at is null
		  and not m.muted
		  and not m.disabled
		  and (i.last_alert_at is null
		       or i.last_alert_at < now() - make_interval(secs => $1::double precision))
		  and exists (
		        select 1 from channels c
		        where c.enabled
		          -- Only channels this monitor's project actually routes to: an
		          -- unroutable incident must stop retrying, not retry forever.
		          and (c.project_id = m.project_id or exists (
		                select 1 from project_channels pc
		                where pc.channel_id = c.id and pc.project_id = m.project_id))
		          and not exists (
		                select 1 from notifications n
		                where n.dedup_key = 'incident:' || i.id || ':open'
		                  and n.channel_id = c.id))
		order by i.opened_at`, d.alertRetry.Seconds())
	if err != nil {
		return 0, fmt.Errorf("select open incidents: %w", err)
	}

	type inc struct {
		id                  int64
		kind                string
		openedAt            time.Time
		slug, env           string
		projectID           int64
		lastBeat            *time.Time
		lastProgress        *int64
		windowStart         *time.Time
		everySecs, stallSec int64
	}
	var pending []inc
	for rows.Next() {
		var i inc
		if err := rows.Scan(&i.id, &i.kind, &i.openedAt, &i.slug, &i.projectID, &i.env,
			&i.lastBeat, &i.lastProgress, &i.windowStart, &i.everySecs, &i.stallSec); err != nil {
			rows.Close()
			return 0, err
		}
		pending = append(pending, i)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for _, i := range pending {
		ev := alert.Event{
			DedupKey:    fmt.Sprintf("incident:%d:open", i.id),
			Severity:    alert.SeverityCritical,
			Monitor:     i.slug,
			Environment: i.env,
			ProjectID:   i.projectID,
			URL:         monitorURL(d.baseURL, i.slug),
			Fields:      map[string]string{},
		}
		switch i.kind {
		case "stalled":
			frozen := "unknown"
			if i.windowStart != nil {
				frozen = i.openedAt.Sub(*i.windowStart).Round(time.Second).String()
			}
			ev.Kind = "monitor.stalled"
			ev.Status = string(StatusStalled)
			ev.Title = fmt.Sprintf("%s STALLED", i.slug)
			ev.Text = fmt.Sprintf(
				"🟠 %s (%s) is STALLED — still beating, but no progress for %s. Loop is alive and doing nothing.",
				i.slug, i.env, frozen)
			ev.Fields["frozen_for"] = frozen
			ev.Fields["progress"] = fmt.Sprint(derefInt(i.lastProgress))
			ev.Fields["stall_window_s"] = fmt.Sprint(i.stallSec)
		default:
			silence := "unknown"
			if i.lastBeat != nil {
				silence = i.openedAt.Sub(*i.lastBeat).Round(time.Second).String()
			}
			ev.Kind = "monitor.missing"
			ev.Status = string(StatusMissing)
			ev.Title = fmt.Sprintf("%s MISSING", i.slug)
			ev.Text = fmt.Sprintf(
				"🔴 %s (%s) is MISSING — no heartbeat for %s (expected every %ds).",
				i.slug, i.env, silence, i.everySecs)
			ev.Fields["silent_for"] = silence
			ev.Fields["expected_every_s"] = fmt.Sprint(i.everySecs)
			if i.lastBeat != nil {
				ev.Fields["last_beat_at"] = i.lastBeat.Format(time.RFC3339)
			}
		}

		d.alerter.Notify(ctx, ev)
		// Records the last *attempt*, which is what rate-limits retries. Whether the
		// alert actually arrived is recorded in notifications by the alerter.
		if _, err := d.db.Exec(ctx,
			`update incidents set last_alert_at = now() where id = $1`, i.id); err != nil {
			return len(pending), err
		}
	}
	return len(pending), nil
}

// View is one row of the monitors wall.
type View struct {
	Slug         string     `json:"monitor"`
	Environment  string     `json:"environment"`
	Kind         string     `json:"kind"`
	Status       string     `json:"status"`
	LastBeatAt   *time.Time `json:"last_beat_at"`
	LastProgress *int64     `json:"last_progress"`
	NextExpected *time.Time `json:"next_expected_at"`
	EverySecs    int64      `json:"expected_every_s"`
	Muted        bool       `json:"muted"`
	OpenSince    *time.Time `json:"open_incident_since,omitempty"`
}

func (e *Engine) List(ctx context.Context) ([]View, error) {
	rows, err := e.db.Query(ctx, `
		select m.slug, coalesce(ms.environment, 'production'), m.kind,
		       coalesce(ms.status, 'waiting'), ms.last_beat_at, ms.last_progress,
		       ms.next_expected_at, coalesce((m.config->>'expected_every_secs')::bigint, 60),
		       m.muted,
		       (select opened_at from incidents i
		         where i.monitor_id = m.id and i.environment = coalesce(ms.environment, 'production')
		           and i.resolved_at is null limit 1)
		from monitors m
		left join monitor_state ms on ms.monitor_id = m.id
		where not m.disabled
		order by (coalesce(ms.status, 'waiting') in ('missing', 'stalled')) desc, m.slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []View
	for rows.Next() {
		var v View
		if err := rows.Scan(&v.Slug, &v.Environment, &v.Kind, &v.Status, &v.LastBeatAt,
			&v.LastProgress, &v.NextExpected, &v.EverySecs, &v.Muted, &v.OpenSince); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func derefInt(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
