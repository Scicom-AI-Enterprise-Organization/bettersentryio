package api

// Issue alerting: a brand-new issue notifies the alert channels the moment it
// is ingested, riding the same queue, Teams card format and dedup ledger as
// monitor incidents. New-issue only, deliberately: alerting every occurrence
// would turn a crash loop into a Teams flood — the issue page carries the
// count, the alert carries the news.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/alert"
	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/events"
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

// notifyNewIssue announces a first-seen issue. Failures are the alerter's
// problem (queued, retried, deduped) — ingest never waits on Teams.
func (s *Server) notifyNewIssue(ctx context.Context, projectID int64, res events.Ingested) {
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
	s.alerter.Notify(ctx, alert.Event{
		Kind:        "issue.new",
		Monitor:     slug,
		Environment: res.Environment,
		Status:      "new",
		Severity:    severityFor(res.Level),
		Title:       fmt.Sprintf("New issue in %s: %s", name, res.Title),
		Text:        fmt.Sprintf("%s\nin %s · %s · first seen just now", res.Title, res.Culprit, res.Environment),
		URL:         url,
		Fields: map[string]string{
			"app":         slug,
			"level":       res.Level,
			"environment": res.Environment,
			"culprit":     res.Culprit,
		},
		DedupKey: fmt.Sprintf("issue:%d:new", res.IssueID),
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
