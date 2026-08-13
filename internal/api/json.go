package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/monitor"
)

// This file is the read API the Next.js frontend consumes. monitor.Row exposes
// derived values as methods, which JSON cannot see, so each response gets an
// explicit DTO rather than leaking the storage shape to the UI.

type monitorDTO struct {
	Slug           string     `json:"slug"`
	AppSlug        string     `json:"app"`
	AppName        string     `json:"app_name"`
	Environment    string     `json:"environment"`
	Kind           string     `json:"kind"`
	Status         string     `json:"status"`
	LastBeatAt     *time.Time `json:"last_beat_at"`
	LastProgress   *int64     `json:"last_progress"`
	NextExpectedAt *time.Time `json:"next_expected_at"`
	EverySecs      int64      `json:"every_secs"`
	GraceSecs      int64      `json:"grace_secs"`
	StallSecs      int64      `json:"stall_window_secs"`
	Muted          bool       `json:"muted"`
	CreatedAt      time.Time  `json:"created_at"`
	OpenSince      *time.Time `json:"open_incident_since"`
	OpenSecs       *int64     `json:"open_incident_secs"`
	Uptime         float64    `json:"uptime_pct"`
	ObservedSecs   int64      `json:"uptime_observed_secs"`
	Beats24h       int64      `json:"beats_24h"`
	Activity       []bucket   `json:"activity"`
}

type bucket struct {
	At            time.Time `json:"at"`
	Beats         int       `json:"beats"`
	ProgressDelta int64     `json:"progress_delta"`
}

type incidentDTO struct {
	ID          int64      `json:"id"`
	Monitor     string     `json:"monitor"`
	Environment string     `json:"environment"`
	Kind        string     `json:"kind"`
	OpenedAt    time.Time  `json:"opened_at"`
	ResolvedAt  *time.Time `json:"resolved_at"`
	DurationS   int64      `json:"duration_secs"`
	Delivered   int        `json:"alerts_delivered"`
}

func toMonitorDTO(r monitor.Row) monitorDTO {
	// How long the incident has been open, measured by the engine. The UI must not
	// compute this from the browser clock: three machines' clocks disagree, and this is
	// the number an operator acts on.
	var openSecs *int64
	if r.OpenSince != nil {
		n := int64(time.Since(*r.OpenSince).Seconds())
		if n < 0 {
			n = 0
		}
		openSecs = &n
	}

	return monitorDTO{
		Slug: r.Slug, AppSlug: r.AppSlug, AppName: r.AppName,
		Environment: r.Environment, Kind: r.Kind, Status: r.Status,
		LastBeatAt: r.LastBeatAt, LastProgress: r.LastProgress, NextExpectedAt: r.NextExpectedAt,
		EverySecs: r.EverySecs, GraceSecs: r.GraceSecs, StallSecs: r.StallSecs,
		Muted: r.Muted, CreatedAt: r.CreatedAt, OpenSince: r.OpenSince, OpenSecs: openSecs,
		Uptime:       r.Uptime24h(),
		ObservedSecs: int64(r.Observed24h().Seconds()),
		Beats24h:     r.Beats24h,
		Activity:     toBuckets(r.Spark),
	}
}

func toBuckets(in []monitor.Bucket) []bucket {
	out := make([]bucket, 0, len(in))
	for _, b := range in {
		out = append(out, bucket{At: b.WindowStart, Beats: b.Beats, ProgressDelta: b.ProgressDelta})
	}
	return out
}

func toIncidentDTOs(in []monitor.Incident) []incidentDTO {
	out := make([]incidentDTO, 0, len(in))
	for _, i := range in {
		out = append(out, incidentDTO{
			ID: i.ID, Monitor: i.Monitor, Environment: i.Environment, Kind: i.Kind,
			OpenedAt: i.OpenedAt, ResolvedAt: i.ResolvedAt,
			DurationS: int64(i.Duration().Seconds()), Delivered: i.Delivered,
		})
	}
	return out
}

// handleOverview is one round trip for the monitors page: counts plus every row.
func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "sign in, or pass an ingest key (X-BSIO-Key or ?key=)")
		return
	}
	summary, err := s.engine.Summary(r.Context())
	if err != nil {
		s.log.Error("summary failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	rows, err := s.engine.Rows(r.Context())
	if err != nil {
		s.log.Error("rows failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	out := make([]monitorDTO, 0, len(rows))
	for _, row := range rows {
		out = append(out, toMonitorDTO(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"summary": map[string]int{
			"total": summary.Total, "ok": summary.OK, "late": summary.Late,
			"missing": summary.Missing, "stalled": summary.Stalled,
			"waiting": summary.Waiting, "open_incidents": summary.OpenIncidents,
			"unhealthy": summary.Unhealthy(),
		},
		"monitors": out,
	})
}

func (s *Server) handleMonitorDetail(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "sign in, or pass an ingest key")
		return
	}
	slug := r.PathValue("slug")
	row, err := s.engine.Row(r.Context(), slug)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "no such monitor")
		return
	}
	if err != nil {
		s.log.Error("monitor detail failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	activity, err := s.engine.Activity(r.Context(), row.ID, 2*time.Hour)
	if err != nil {
		s.log.Error("activity failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	incidents, err := s.engine.Incidents(r.Context(), row.ID, 25)
	if err != nil {
		s.log.Error("incidents failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	cfg, err := s.engine.Config(r.Context(), slug)
	if err != nil {
		s.log.Error("config failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}

	dto := toMonitorDTO(row)
	dto.Activity = toBuckets(activity) // detail shows 2h, not the 1h sparkline
	writeJSON(w, http.StatusOK, map[string]any{
		"monitor":   dto,
		"config":    cfg,
		"incidents": toIncidentDTOs(incidents),
	})
}

func (s *Server) handleIncidents(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "sign in, or pass an ingest key")
		return
	}
	list, err := s.engine.Incidents(r.Context(), 0, 100)
	if err != nil {
		s.log.Error("incidents failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"incidents": toIncidentDTOs(list)})
}

func (s *Server) handleMute(w http.ResponseWriter, r *http.Request) {
	if !s.mayAdminister(r) {
		writeErr(w, http.StatusUnauthorized, "sign in, or pass an ingest key")
		return
	}
	slug := r.PathValue("slug")
	muted := r.URL.Query().Get("muted") != "false"
	if err := s.engine.SetMuted(r.Context(), slug, muted); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "no such monitor")
			return
		}
		s.log.Error("mute failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"monitor": slug, "muted": muted})
}
