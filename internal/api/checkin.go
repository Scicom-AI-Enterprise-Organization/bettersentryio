package api

// Sentry crons check-ins, bridged onto the native beat pipeline (closes E3 in
// docs/design/sentry-compat.md). The mapping is deliberate, not a shim:
//
//	status ok     -> a beat. "Beat only on success" is already this platform's
//	                 cron doctrine, so a cron that stops running OR keeps
//	                 failing goes MISSING and pages — no new state machine.
//	status error  -> an error issue through the normal ingest path, one issue
//	                 per (monitor, environment) via an explicit fingerprint, so
//	                 failures alert immediately and regressions read as such.
//	in_progress   -> ignored. It marks the start of a run; the ok/error that
//	                 follows is the signal, and beating on it would teach the
//	                 adaptive schedule the job's *duration*, not its period.
//
// The beat deadline needs an expected period. monitor_config carries one for
// SDKs that send it (interval directly; crontab estimated — the deadline only
// needs the right order of magnitude, grace absorbs the rest). Without config,
// the gap between successive ok check-ins is the period: self-calibrating for
// any schedule, one conservative 24h default until the second check-in.

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/events"
	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/monitor"
)

type checkInBody struct {
	MonitorSlug   string  `json:"monitor_slug"`
	Status        string  `json:"status"` // in_progress | ok | error
	Duration      float64 `json:"duration"`
	Environment   string  `json:"environment"`
	MonitorConfig *struct {
		Schedule struct {
			Type  string          `json:"type"` // crontab | interval
			Value json.RawMessage `json:"value"`
			Unit  string          `json:"unit"`
		} `json:"schedule"`
		CheckinMargin float64 `json:"checkin_margin"` // minutes
	} `json:"monitor_config"`
}

func (s *Server) handleCheckInItem(ctx context.Context, projectID int64, payload []byte) {
	var c checkInBody
	if err := json.Unmarshal(payload, &c); err != nil || c.MonitorSlug == "" {
		s.log.Debug("check_in item unusable", "project", projectID, "err", err)
		return
	}
	env := c.Environment
	if env == "" {
		env = "production"
	}

	switch c.Status {
	case "ok":
		every, grace := s.checkInSchedule(ctx, projectID, c, env)
		res, err := s.engine.Beat(ctx, monitor.BeatRequest{
			ProjectID:     projectID,
			Slug:          c.MonitorSlug,
			Environment:   env,
			ExpectedEvery: every,
			Grace:         grace,
			StallWindow:   -1, // check-ins carry no progress counter
		})
		if err != nil {
			s.log.Error("check_in beat failed", "monitor", c.MonitorSlug, "err", err)
			return
		}
		if res.Created {
			s.log.Info("monitor created from check_in", "monitor", c.MonitorSlug, "every", every)
		}

	case "error":
		msg := fmt.Sprintf("Cron %s failed", c.MonitorSlug)
		if c.Duration > 0 {
			msg = fmt.Sprintf("%s after %.1fs", msg, c.Duration)
		}
		ev := events.Event{
			Level:       "error",
			Message:     msg,
			Transaction: c.MonitorSlug,
			Environment: env,
			// One issue per monitor+environment: the message varies by duration,
			// so grouping must not hang off it.
			Fingerprint: []string{"cron", c.MonitorSlug},
			Tags:        events.Tags{"monitor": c.MonitorSlug, "mechanism": "cron"},
		}
		res, err := s.events.Ingest(ctx, projectID, &ev)
		if err != nil {
			s.log.Error("check_in failure ingest failed", "monitor", c.MonitorSlug, "err", err)
			return
		}
		if res.IsNew {
			s.log.Info("new issue", "issue", res.IssueID, "culprit", res.Culprit, "via", "check_in")
		}
		s.notifyIssue(ctx, projectID, res)
	}
}

func (s *Server) checkInSchedule(ctx context.Context, projectID int64, c checkInBody, env string) (every, grace time.Duration) {
	if c.MonitorConfig != nil {
		margin := time.Duration(c.MonitorConfig.CheckinMargin * float64(time.Minute))
		sch := c.MonitorConfig.Schedule
		switch sch.Type {
		case "interval":
			var n float64
			if json.Unmarshal(sch.Value, &n) == nil {
				if d := intervalDuration(n, sch.Unit); d > 0 {
					return d, checkInGrace(d, margin)
				}
			}
		case "crontab":
			var expr string
			if json.Unmarshal(sch.Value, &expr) == nil {
				if d := cronPeriod(expr); d > 0 {
					return d, checkInGrace(d, margin)
				}
			}
		}
	}

	// No config: the gap between successive ok check-ins IS the schedule.
	var last *time.Time
	_ = s.db.QueryRow(ctx, `
		select ms.last_beat_at from monitor_state ms
		join monitors m on m.id = ms.monitor_id
		where m.project_id = $1 and m.slug = $2 and ms.environment = $3`,
		projectID, c.MonitorSlug, env).Scan(&last)
	if last != nil {
		gap := time.Since(*last)
		if gap < time.Minute {
			gap = time.Minute
		}
		if gap > 8*24*time.Hour {
			gap = 8 * 24 * time.Hour
		}
		return gap, checkInGrace(gap, 0)
	}
	return 24 * time.Hour, 6 * time.Hour
}

func checkInGrace(every, margin time.Duration) time.Duration {
	if margin > 0 {
		return margin
	}
	g := every / 4
	if g < 5*time.Minute {
		g = 5 * time.Minute
	}
	return g
}

func intervalDuration(n float64, unit string) time.Duration {
	if n <= 0 {
		return 0
	}
	base := map[string]time.Duration{
		"minute": time.Minute,
		"hour":   time.Hour,
		"day":    24 * time.Hour,
		"week":   7 * 24 * time.Hour,
		"month":  31 * 24 * time.Hour,
		"year":   365 * 24 * time.Hour,
	}[unit]
	if base == 0 {
		return 0
	}
	return time.Duration(n * float64(base))
}

// cronPeriod estimates a 5-field crontab expression's nominal period. It is not
// a scheduler: the beat deadline needs the right order of magnitude, and forms
// it cannot read (lists, ranges) fall through to the conservative bucket.
func cronPeriod(expr string) time.Duration {
	f := strings.Fields(expr)
	if len(f) != 5 {
		return 0
	}
	min, hour, dom, _, dow := f[0], f[1], f[2], f[3], f[4]
	if strings.HasPrefix(min, "*/") {
		if n, err := strconv.Atoi(min[2:]); err == nil && n > 0 {
			return time.Duration(n) * time.Minute
		}
	}
	if min == "*" {
		return time.Minute
	}
	if strings.HasPrefix(hour, "*/") {
		if n, err := strconv.Atoi(hour[2:]); err == nil && n > 0 {
			return time.Duration(n) * time.Hour
		}
	}
	if hour == "*" {
		return time.Hour // fixed minute, every hour
	}
	if dow != "*" {
		return 7 * 24 * time.Hour
	}
	if dom != "*" {
		return 31 * 24 * time.Hour
	}
	return 24 * time.Hour
}
