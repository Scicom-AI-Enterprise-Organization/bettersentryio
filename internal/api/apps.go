package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/store"
)

type appDTO struct {
	ID           int64      `json:"id"`
	Slug         string     `json:"slug"`
	Name         string     `json:"name"`
	Platform     string     `json:"platform"`
	CreatedAt    time.Time  `json:"created_at"`
	Key          string     `json:"ingest_key"`
	Monitors     int        `json:"monitors"`
	Unhealthy    int        `json:"unhealthy"`
	LastBeatAt   *time.Time `json:"last_beat_at"`
	OpenIncident bool       `json:"open_incident"`
	OpenIssues   int        `json:"open_issues"`
	LastEventAt  *time.Time `json:"last_event_at"`
	Connected    bool       `json:"connected"`
	// 0 = keep forever, which is the default: deleting history is opt-in.
	RetentionDays int `json:"retention_days"`
}

func (s *Server) handleApps(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "sign in, or pass an ingest key")
		return
	}
	apps, err := s.engine.Apps(r.Context())
	if err != nil {
		s.log.Error("list apps failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	out := make([]appDTO, 0, len(apps))
	for _, a := range apps {
		out = append(out, appDTO{
			ID: a.ID, Slug: a.Slug, Name: a.Name, Platform: a.Platform, CreatedAt: a.CreatedAt, Key: a.Key,
			Monitors: a.Monitors, Unhealthy: a.Unhealthy, LastBeatAt: a.LastBeatAt,
			OpenIncident: a.OpenIncident, OpenIssues: a.OpenIssues,
			LastEventAt: a.LastEventAt, Connected: a.Connected(),
			RetentionDays: a.RetentionDays,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"apps": out})
}

// handleCreateApp registers an app and returns it with a freshly minted ingest key.
func (s *Server) handleCreateApp(w http.ResponseWriter, r *http.Request) {
	if !s.mayAdminister(r) {
		writeErr(w, http.StatusUnauthorized, "creating an app requires the operator API token")
		return
	}

	var body struct {
		Name     string `json:"name"`
		Platform string `json:"platform"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "expected a JSON body with a name")
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	if len([]rune(body.Name)) > 128 {
		writeErr(w, http.StatusBadRequest, "name must be 128 characters or fewer")
		return
	}
	if store.Slugify(body.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name must contain at least one letter or digit")
		return
	}

	boot, err := s.db.CreateProject(r.Context(), body.Name, sanitizePlatform(body.Platform))
	if errors.Is(err, store.ErrSlugTaken) {
		writeErr(w, http.StatusConflict, "an app with that name already exists")
		return
	}
	if err != nil {
		s.log.Error("create app failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "could not create the app")
		return
	}
	s.log.Info("app created", "slug", boot.ProjectSlug)

	writeJSON(w, http.StatusCreated, map[string]any{
		"slug":       boot.ProjectSlug,
		"name":       body.Name,
		"platform":   sanitizePlatform(body.Platform),
		"ingest_key": boot.PublicKey,
	})
}

// handleAppDetail returns one app plus its monitors, which is what the app page and
// the "has it connected yet?" poll both need.
func (s *Server) handleAppDetail(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "sign in, or pass an ingest key")
		return
	}
	slug := r.PathValue("slug")
	app, ok, err := s.engine.App(r.Context(), slug)
	if err != nil {
		s.log.Error("app detail failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "no such app")
		return
	}
	rows, err := s.engine.RowsForApp(r.Context(), slug)
	if err != nil {
		s.log.Error("app monitors failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	monitors := make([]monitorDTO, 0, len(rows))
	for _, row := range rows {
		monitors = append(monitors, toMonitorDTO(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"app": appDTO{
			ID: app.ID, Slug: app.Slug, Name: app.Name, Platform: app.Platform, CreatedAt: app.CreatedAt, Key: app.Key,
			Monitors: app.Monitors, Unhealthy: app.Unhealthy, LastBeatAt: app.LastBeatAt,
			OpenIncident: app.OpenIncident, OpenIssues: app.OpenIssues,
			LastEventAt: app.LastEventAt, Connected: app.Connected(),
			RetentionDays: app.RetentionDays,
		},
		"monitors": monitors,
	})
}

// handleDeleteApp removes an app and everything under it. Operator-only: an ingest key
// proves a service can report, not that its holder may destroy monitoring history.
func (s *Server) handleDeleteApp(w http.ResponseWriter, r *http.Request) {
	if !s.mayAdminister(r) {
		writeErr(w, http.StatusUnauthorized, "deleting an app requires the operator API token")
		return
	}
	slug := r.PathValue("slug")
	monitors, found, err := s.db.DeleteProject(r.Context(), slug)
	if err != nil {
		s.log.Error("delete app failed", "slug", slug, "err", err)
		writeErr(w, http.StatusServiceUnavailable, "could not delete the app")
		return
	}
	if !found {
		writeErr(w, http.StatusNotFound, "no such app")
		return
	}
	s.log.Warn("app deleted", "slug", slug, "monitors_removed", monitors)
	writeJSON(w, http.StatusOK, map[string]any{"slug": slug, "monitors_removed": monitors})
}

// sanitizePlatform keeps the stored value to a short identifier. It is only ever used
// to pick a logo and a snippet, so anything unrecognised degrades to "" rather than
// being rejected — a new UI must not be able to fail app creation over a label.
func sanitizePlatform(in string) string {
	in = strings.ToLower(strings.TrimSpace(in))
	if len(in) > 32 {
		return ""
	}
	for _, r := range in {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' {
			return ""
		}
	}
	return in
}
