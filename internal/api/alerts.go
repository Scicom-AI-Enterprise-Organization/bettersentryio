package api

// Issue alerting: a brand-new issue notifies the alert channels the moment it
// is ingested, riding the same queue, Teams card format and dedup ledger as
// monitor incidents. New-issue only, deliberately: alerting every occurrence
// would turn a crash loop into a Teams flood — the issue page carries the
// count, the alert carries the news.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/alert"
	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/events"
	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/store"
)

func severityFor(level string) string {
	switch level {
	case "fatal", "error":
		return alert.SeverityCritical
	case "warning":
		return alert.SeverityWarning
	default:
		return alert.SeverityOK
	}
}

// notifyIssue announces a first-seen issue or a regression (an event arriving
// at an issue somebody had marked resolved). Failures are the alerter's
// problem (queued, retried, deduped) — ingest never waits on Teams.
func (s *Server) notifyIssue(ctx context.Context, projectID int64, res events.Ingested) {
	if !res.IsNew && !res.Reopened {
		return
	}
	slug, name, err := s.db.ProjectMeta(ctx, projectID)
	if err != nil || slug == "" {
		slug = fmt.Sprintf("project-%d", projectID)
	}
	if name == "" {
		name = slug
	}
	url := ""
	if s.baseURL != "" {
		url = fmt.Sprintf("%s/apps/%s/errors/%d", s.baseURL, slug, res.IssueID)
	}

	kind, status := "issue.new", "new"
	title := fmt.Sprintf("New issue in %s: %s", name, res.Title)
	text := fmt.Sprintf("%s\nin %s · %s · first seen just now", res.Title, res.Culprit, res.Environment)
	dedup := fmt.Sprintf("issue:%d:new", res.IssueID)
	if res.Reopened {
		// A regression is worse news than a new bug: something we called fixed
		// is happening again. times_seen in the key makes each reopen alert once.
		kind, status = "issue.regression", "regression"
		title = fmt.Sprintf("Regression in %s: %s", name, res.Title)
		text = fmt.Sprintf("%s\nin %s · %s · was resolved, happening again (seen %d× total)",
			res.Title, res.Culprit, res.Environment, res.TimesSeen)
		dedup = fmt.Sprintf("issue:%d:reopen:%d", res.IssueID, res.TimesSeen)
	}

	s.alerter.Notify(ctx, alert.Event{
		Kind:        kind,
		Monitor:     slug,
		Environment: res.Environment,
		Status:      status,
		Severity:    severityFor(res.Level),
		Title:       title,
		Text:        text,
		URL:         url,
		Fields: map[string]string{
			"app":         slug,
			"level":       res.Level,
			"environment": res.Environment,
			"culprit":     res.Culprit,
		},
		DedupKey: dedup,
	})
}

/* ---- Teams channel settings -------------------------------------------------
 * One named channel ("teams") managed by one field in the UI. The channels
 * table supports more; the product supports exactly what was asked for.
 */

const teamsChannelName = "teams"

func maskURL(u string) string {
	// Show enough to recognize the webhook, never the whole capability URL.
	if len(u) <= 40 {
		return u
	}
	return u[:40] + "…"
}

func (s *Server) handleGetTeamsAlert(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "sign in, or pass an ingest key")
		return
	}
	_, config, enabled, found, err := s.db.ChannelByName(r.Context(), teamsChannelName)
	if err != nil {
		s.log.Error("channel lookup failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"configured": found && enabled && config["url"] != "",
		"url_masked": maskURL(config["url"]),
	})
}

// handleSetTeamsAlert upserts the Teams webhook; an empty URL disables the
// channel without forgetting it.
func (s *Server) handleSetTeamsAlert(w http.ResponseWriter, r *http.Request) {
	if !s.mayAdminister(r) {
		writeErr(w, http.StatusUnauthorized, "changing alert channels requires the operator API token")
		return
	}
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "expected a JSON body with a url")
		return
	}
	body.URL = strings.TrimSpace(body.URL)

	if body.URL == "" {
		if err := s.db.SetChannelEnabled(r.Context(), teamsChannelName, false); err != nil {
			s.log.Error("disable teams channel failed", "err", err)
			writeErr(w, http.StatusServiceUnavailable, "could not update the channel")
			return
		}
		s.log.Info("teams alerts disabled")
		writeJSON(w, http.StatusOK, map[string]any{"configured": false, "url_masked": ""})
		return
	}

	if !strings.HasPrefix(body.URL, "https://") {
		writeErr(w, http.StatusBadRequest, "a Teams incoming-webhook URL starts with https://")
		return
	}
	cfg, _ := json.Marshal(map[string]string{"url": body.URL})
	if err := s.db.EnsureChannel(r.Context(), teamsChannelName, "teams", string(cfg)); err != nil {
		s.log.Error("save teams channel failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "could not save the channel")
		return
	}
	s.log.Info("teams alerts configured")
	writeJSON(w, http.StatusOK, map[string]any{"configured": true, "url_masked": maskURL(body.URL)})
}

/* ---- named channels CRUD ------------------------------------------------------
 * The table view: several webhooks, each with a human name, each deletable
 * without touching the others. The alerter fans out to every enabled row.
 */

type channelDTO struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	URLMasked string `json:"url_masked"`
	Enabled   bool   `json:"enabled"`
}

var allowedChannelTypes = map[string]bool{"teams": true, "slack": true, "webhook": true}

func (s *Server) handleListChannels(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "sign in, or pass an ingest key")
		return
	}
	rows, err := s.db.ListChannels(r.Context())
	if err != nil {
		s.log.Error("list channels failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	out := make([]channelDTO, 0, len(rows))
	for _, c := range rows {
		out = append(out, channelDTO{
			ID: c.ID, Name: c.Name, Type: c.Kind, URLMasked: maskURL(c.URL), Enabled: c.Enabled,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": out})
}

func (s *Server) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	if !s.mayAdminister(r) {
		writeErr(w, http.StatusUnauthorized, "changing alert channels requires the operator API token")
		return
	}
	var body struct {
		Name string `json:"name"`
		Type string `json:"type"`
		URL  string `json:"url"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "expected a JSON body with name and url")
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	body.URL = strings.TrimSpace(body.URL)
	if body.Type == "" {
		body.Type = "teams"
	}
	switch {
	case body.Name == "" || len(body.Name) > 64:
		writeErr(w, http.StatusBadRequest, "name is required (max 64 chars)")
		return
	case !allowedChannelTypes[body.Type]:
		writeErr(w, http.StatusBadRequest, "type must be teams, slack or webhook")
		return
	case !strings.HasPrefix(body.URL, "https://"):
		writeErr(w, http.StatusBadRequest, "a webhook URL starts with https://")
		return
	}
	id, err := s.db.CreateChannel(r.Context(), body.Name, body.Type, body.URL)
	if errors.Is(err, store.ErrChannelNameTaken) {
		writeErr(w, http.StatusConflict, "a channel with that name already exists")
		return
	}
	if err != nil {
		s.log.Error("create channel failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "could not create the channel")
		return
	}
	s.log.Info("alert channel created", "name", body.Name, "type", body.Type)
	writeJSON(w, http.StatusCreated, channelDTO{
		ID: id, Name: body.Name, Type: body.Type, URLMasked: maskURL(body.URL), Enabled: true,
	})
}

func (s *Server) handleUpdateChannel(w http.ResponseWriter, r *http.Request) {
	if !s.mayAdminister(r) {
		writeErr(w, http.StatusUnauthorized, "changing alert channels requires the operator API token")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "channel id must be an integer")
		return
	}
	var body struct {
		Name    *string `json:"name"`
		URL     *string `json:"url"`
		Enabled *bool   `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "expected a JSON body")
		return
	}
	if body.Name != nil {
		trimmed := strings.TrimSpace(*body.Name)
		if trimmed == "" || len(trimmed) > 64 {
			writeErr(w, http.StatusBadRequest, "name is required (max 64 chars)")
			return
		}
		body.Name = &trimmed
	}
	if body.URL != nil {
		trimmed := strings.TrimSpace(*body.URL)
		if !strings.HasPrefix(trimmed, "https://") {
			writeErr(w, http.StatusBadRequest, "a webhook URL starts with https://")
			return
		}
		body.URL = &trimmed
	}
	found, err := s.db.UpdateChannel(r.Context(), id, body.Name, body.URL, body.Enabled)
	if errors.Is(err, store.ErrChannelNameTaken) {
		writeErr(w, http.StatusConflict, "a channel with that name already exists")
		return
	}
	if err != nil {
		s.log.Error("update channel failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "could not update the channel")
		return
	}
	if !found {
		writeErr(w, http.StatusNotFound, "no such channel")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": id})
}

func (s *Server) handleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	if !s.mayAdminister(r) {
		writeErr(w, http.StatusUnauthorized, "changing alert channels requires the operator API token")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "channel id must be an integer")
		return
	}
	found, err := s.db.DeleteChannel(r.Context(), id)
	if err != nil {
		s.log.Error("delete channel failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "could not delete the channel")
		return
	}
	if !found {
		writeErr(w, http.StatusNotFound, "no such channel")
		return
	}
	s.log.Warn("alert channel deleted", "id", id)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}
