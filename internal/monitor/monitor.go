// Package monitor implements the liveness engine: beats in, state transitions
// out. Arrival-side transitions (recovery) live here; absence-side transitions
// (missing, stalled) live in the detector.
package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/alert"
	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/store"
)

type Status string

const (
	StatusWaiting Status = "waiting"
	StatusOK      Status = "ok"
	StatusLate    Status = "late"
	StatusMissing Status = "missing"
	StatusStalled Status = "stalled"
)

const rollupWindow = 5 * time.Minute

// Config is the JSON stored in monitors.config. Durations are seconds so the
// detector's SQL can read them directly without a second source of truth.
type Config struct {
	ExpectedEverySecs int64 `json:"expected_every_secs,omitempty"`
	GraceSecs         int64 `json:"grace_secs,omitempty"`
	// StallWindowSecs > 0 arms stall detection; it only ever fires once a
	// progress counter has been reported at least once. -1 disables it outright.
	StallWindowSecs int64 `json:"stall_window_secs,omitempty"`
}

func (c Config) expectedEvery() time.Duration {
	if c.ExpectedEverySecs <= 0 {
		return time.Minute
	}
	return time.Duration(c.ExpectedEverySecs) * time.Second
}

func (c Config) grace() time.Duration {
	if c.GraceSecs <= 0 {
		if g := c.expectedEvery(); g > 30*time.Second {
			return g
		}
		return 30 * time.Second
	}
	return time.Duration(c.GraceSecs) * time.Second
}

// defaultConfig fills in the gaps left by a beat's query-string hints.
func defaultConfig(every, grace, stall time.Duration) Config {
	c := Config{}
	if every > 0 {
		c.ExpectedEverySecs = int64(every.Seconds())
	} else {
		c.ExpectedEverySecs = 60
	}
	if grace > 0 {
		c.GraceSecs = int64(grace.Seconds())
	} else {
		c.GraceSecs = int64(Config{ExpectedEverySecs: c.ExpectedEverySecs}.grace().Seconds())
	}
	switch {
	case stall < 0:
		c.StallWindowSecs = -1
	case stall > 0:
		c.StallWindowSecs = int64(stall.Seconds())
	default:
		w := 3 * time.Duration(c.ExpectedEverySecs) * time.Second
		if w < 2*time.Minute {
			w = 2 * time.Minute
		}
		c.StallWindowSecs = int64(w.Seconds())
	}
	return c
}

type Engine struct {
	db      *store.DB
	alerter *alert.Alerter
	log     *slog.Logger
	baseURL string
	now     func() time.Time
}

func NewEngine(db *store.DB, a *alert.Alerter, log *slog.Logger, baseURL string) *Engine {
	return &Engine{
		db: db, alerter: a, log: log, baseURL: baseURL,
		now: func() time.Time { return time.Now().UTC() },
	}
}

// SetClock replaces the engine's notion of now. Tests use it to simulate beats
// arriving over time, which is the only way to exercise "beats are fresh but
// progress is frozen" without waiting in real time.
func (e *Engine) SetClock(f func() time.Time) { e.now = f }

type BeatRequest struct {
	ProjectID     int64
	Slug          string
	Environment   string
	Progress      *int64
	ExpectedEvery time.Duration
	Grace         time.Duration
	StallWindow   time.Duration
}

type BeatResult struct {
	Monitor      string    `json:"monitor"`
	Environment  string    `json:"environment"`
	Status       Status    `json:"status"`
	Created      bool      `json:"created,omitempty"`
	Recovered    bool      `json:"recovered,omitempty"`
	NextExpected time.Time `json:"next_expected_at"`
}

// Beat records one heartbeat. It is the only write path that can clear a
// missing/stalled state, which is why recovery notification happens here rather
// than on the detector tick: recovery should be immediate, not up to a tick late.
func (e *Engine) Beat(ctx context.Context, req BeatRequest) (BeatResult, error) {
	if req.Environment == "" {
		req.Environment = "production"
	}
	now := e.now()

	tx, err := e.db.Begin(ctx)
	if err != nil {
		return BeatResult{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	monitorID, cfg, created, err := ensureMonitor(ctx, tx, req)
	if err != nil {
		return BeatResult{}, err
	}

	// Row lock serializes concurrent beats for this monitor, so the read-modify-write
	// of the progress anchor below cannot interleave.
	var (
		oldStatus   Status
		lastBeat    *time.Time
		lastProg    *int64
		anchorProg  *int64
		anchorSince *time.Time
	)
	err = tx.QueryRow(ctx, `
		select status, last_beat_at, last_progress, progress_at_window_start, window_started_at
		from monitor_state where monitor_id = $1 and environment = $2 for update`,
		monitorID, req.Environment,
	).Scan(&oldStatus, &lastBeat, &lastProg, &anchorProg, &anchorSince)
	haveState := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return BeatResult{}, err
	}

	nextAt := now.Add(cfg.expectedEvery())
	nextLatest := nextAt.Add(cfg.grace())

	// The anchor only moves when the counter moves. A counter that went backwards
	// means the process restarted, which is activity, not a stall.
	newProg, newAnchorProg, newAnchorSince := lastProg, anchorProg, anchorSince
	var progressDelta int64
	progressMoved := false
	if req.Progress != nil {
		p := *req.Progress
		progressMoved = lastProg == nil || p != *lastProg
		if lastProg != nil && p > *lastProg {
			progressDelta = p - *lastProg
		}
		newProg = &p
		if progressMoved {
			newAnchorProg, newAnchorSince = &p, &now
		}
	}

	// A beat proves the loop is alive, which clears MISSING. It does not prove the
	// loop is working, so it must not clear STALLED — only moving the progress
	// counter does that. Treating every beat as recovery makes a stalled monitor
	// flap between alert and all-clear on each heartbeat.
	newStatus := StatusOK
	if oldStatus == StatusStalled && !progressMoved {
		newStatus = StatusStalled
	}

	if haveState {
		if _, err := tx.Exec(ctx, `
			update monitor_state set
				status = $9,
				last_beat_at = greatest(last_beat_at, $3),
				last_progress = $4,
				progress_at_window_start = $5,
				window_started_at = $6,
				next_expected_at = $7,
				next_expected_latest = $8,
				updated_at = now()
			where monitor_id = $1 and environment = $2`,
			monitorID, req.Environment, now, newProg, newAnchorProg, newAnchorSince, nextAt, nextLatest, newStatus,
		); err != nil {
			return BeatResult{}, err
		}
	} else {
		oldStatus = StatusWaiting
		if _, err := tx.Exec(ctx, `
			insert into monitor_state (
				monitor_id, environment, status, last_beat_at, last_progress,
				progress_at_window_start, window_started_at, next_expected_at, next_expected_latest)
			values ($1, $2, 'ok', $3, $4, $5, $6, $7, $8)
			on conflict (monitor_id, environment) do nothing`,
			monitorID, req.Environment, now, newProg, newAnchorProg, newAnchorSince, nextAt, nextLatest,
		); err != nil {
			return BeatResult{}, err
		}
	}

	window := now.Truncate(rollupWindow)
	if _, err := tx.Exec(ctx, `
		insert into beat_rollups (monitor_id, environment, window_start, beats, progress_delta)
		values ($1, $2, $3, 1, $4)
		on conflict (monitor_id, environment, window_start) do update
			set beats = beat_rollups.beats + 1,
			    progress_delta = beat_rollups.progress_delta + excluded.progress_delta`,
		monitorID, req.Environment, window, progressDelta,
	); err != nil {
		return BeatResult{}, err
	}

	var recovery *alert.Event
	if newStatus == StatusOK && (oldStatus == StatusMissing || oldStatus == StatusStalled) {
		recovery, err = resolveIncident(ctx, tx, resolveArgs{
			monitorID: monitorID, slug: req.Slug, env: req.Environment,
			now: now, baseURL: e.baseURL, prevStatus: oldStatus,
		})
		if err != nil {
			return BeatResult{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return BeatResult{}, err
	}

	if recovery != nil {
		e.alerter.Notify(ctx, *recovery)
		e.log.Info("monitor recovered", "monitor", req.Slug, "env", req.Environment, "from", string(oldStatus))
	}

	return BeatResult{
		Monitor:      req.Slug,
		Environment:  req.Environment,
		Status:       newStatus,
		Created:      created,
		Recovered:    recovery != nil,
		NextExpected: nextAt,
	}, nil
}

func ensureMonitor(ctx context.Context, tx pgx.Tx, req BeatRequest) (int64, Config, bool, error) {
	var (
		id      int64
		raw     []byte
		cfg     Config
		created bool
	)
	err := tx.QueryRow(ctx,
		`select id, config from monitors where project_id = $1 and slug = $2`,
		req.ProjectID, req.Slug,
	).Scan(&id, &raw)
	switch {
	case err == nil:
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &cfg); err != nil {
				return 0, cfg, false, fmt.Errorf("decode monitor config: %w", err)
			}
		}
		// Hints on a later beat update the schedule — the loop itself is the
		// authority on how often it runs.
		if req.ExpectedEvery > 0 && int64(req.ExpectedEvery.Seconds()) != cfg.ExpectedEverySecs {
			cfg = defaultConfig(req.ExpectedEvery, req.Grace, req.StallWindow)
			blob, _ := json.Marshal(cfg)
			if _, err := tx.Exec(ctx, `update monitors set config = $2::jsonb where id = $1`, id, string(blob)); err != nil {
				return 0, cfg, false, err
			}
		}
		return id, cfg, false, nil

	case errors.Is(err, pgx.ErrNoRows):
		cfg = defaultConfig(req.ExpectedEvery, req.Grace, req.StallWindow)
		blob, _ := json.Marshal(cfg)
		err = tx.QueryRow(ctx, `
			insert into monitors (project_id, slug, kind, name, config)
			values ($1, $2, 'loop', $2, $3::jsonb)
			on conflict (project_id, slug) do nothing
			returning id`,
			req.ProjectID, req.Slug, string(blob),
		).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) { // lost the race; the other beat created it
			if err := tx.QueryRow(ctx,
				`select id, config from monitors where project_id = $1 and slug = $2`,
				req.ProjectID, req.Slug,
			).Scan(&id, &raw); err != nil {
				return 0, cfg, false, err
			}
			_ = json.Unmarshal(raw, &cfg)
			return id, cfg, false, nil
		}
		if err != nil {
			return 0, cfg, false, err
		}
		created = true
		return id, cfg, created, nil

	default:
		return 0, cfg, false, err
	}
}

type resolveArgs struct {
	monitorID  int64
	slug, env  string
	now        time.Time
	baseURL    string
	prevStatus Status
}

// resolveIncident closes the open incident (if any) and returns the recovery
// event to publish after the transaction commits.
func resolveIncident(ctx context.Context, tx pgx.Tx, a resolveArgs) (*alert.Event, error) {
	var (
		id       int64
		kind     string
		openedAt time.Time
	)
	err := tx.QueryRow(ctx, `
		update incidents set resolved_at = $3
		where monitor_id = $1 and environment = $2 and resolved_at is null
		returning id, kind, opened_at`,
		a.monitorID, a.env, a.now,
	).Scan(&id, &kind, &openedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	down := a.now.Sub(openedAt).Round(time.Second)
	ev := alert.Event{
		Kind:        "monitor.recovered",
		DedupKey:    fmt.Sprintf("incident:%d:resolved", id),
		Severity:    alert.SeverityOK,
		Monitor:     a.slug,
		Environment: a.env,
		Status:      string(StatusOK),
		Title:       fmt.Sprintf("%s recovered", a.slug),
		Text: fmt.Sprintf("✅ %s (%s) recovered — was %s for %s",
			a.slug, a.env, kind, down),
		URL: monitorURL(a.baseURL, a.slug),
		Fields: map[string]string{
			"previous_status": string(a.prevStatus),
			"downtime":        down.String(),
		},
	}
	return &ev, nil
}

func monitorURL(baseURL, slug string) string {
	if baseURL == "" {
		return ""
	}
	return fmt.Sprintf("%s/monitors/%s", baseURL, slug)
}
