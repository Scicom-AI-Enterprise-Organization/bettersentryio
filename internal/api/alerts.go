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
	"slices"
	"strconv"
	"strings"
	"time"

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
		ProjectID:   projectID,
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
 * Channels have a scope. The global catalogue (/api/0/channels) holds definitions
 * an operator writes once; a project imports the ones it wants and may also own
 * channels of its own (/api/0/apps/{slug}/channels). The alerter routes an alert
 * to the project's own channels plus its imports — nothing else.
 */

type channelDTO struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	URLMasked string `json:"url_masked"`
	Enabled   bool   `json:"enabled"`
	// Imported is only set on global rows read in a project's context.
	Imported bool `json:"imported"`
}

var allowedChannelTypes = map[string]bool{"teams": true, "slack": true, "webhook": true}

func toChannelDTOs(rows []store.ChannelInfo) []channelDTO {
	out := make([]channelDTO, 0, len(rows))
	for _, c := range rows {
		out = append(out, channelDTO{
			ID: c.ID, Name: c.Name, Type: c.Kind, URLMasked: maskURL(c.URL),
			Enabled: c.Enabled, Imported: c.Imported,
		})
	}
	return out
}

type channelBody struct {
	Name string `json:"name"`
	Type string `json:"type"`
	URL  string `json:"url"`
}

// decodeChannelBody parses and validates a create request, writing the error
// response itself so both scopes reject the same input the same way.
func decodeChannelBody(w http.ResponseWriter, r *http.Request) (channelBody, bool) {
	var body channelBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "expected a JSON body with name and url")
		return body, false
	}
	body.Name = strings.TrimSpace(body.Name)
	body.URL = strings.TrimSpace(body.URL)
	if body.Type == "" {
		body.Type = "teams"
	}
	switch {
	case body.Name == "" || len(body.Name) > 64:
		writeErr(w, http.StatusBadRequest, "name is required (max 64 chars)")
		return body, false
	case !allowedChannelTypes[body.Type]:
		writeErr(w, http.StatusBadRequest, "type must be teams, slack or webhook")
		return body, false
	case !strings.HasPrefix(body.URL, "https://"):
		writeErr(w, http.StatusBadRequest, "a webhook URL starts with https://")
		return body, false
	}
	return body, true
}

type channelPatch struct {
	Name    *string `json:"name"`
	URL     *string `json:"url"`
	Enabled *bool   `json:"enabled"`
}

func decodeChannelPatch(w http.ResponseWriter, r *http.Request) (channelPatch, bool) {
	var body channelPatch
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "expected a JSON body")
		return body, false
	}
	if body.Name != nil {
		trimmed := strings.TrimSpace(*body.Name)
		if trimmed == "" || len(trimmed) > 64 {
			writeErr(w, http.StatusBadRequest, "name is required (max 64 chars)")
			return body, false
		}
		body.Name = &trimmed
	}
	if body.URL != nil {
		trimmed := strings.TrimSpace(*body.URL)
		if !strings.HasPrefix(trimmed, "https://") {
			writeErr(w, http.StatusBadRequest, "a webhook URL starts with https://")
			return body, false
		}
		body.URL = &trimmed
	}
	return body, true
}

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
	writeJSON(w, http.StatusOK, map[string]any{"channels": toChannelDTOs(rows)})
}

func (s *Server) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	if !s.mayAdminister(r) {
		writeErr(w, http.StatusUnauthorized, "changing alert channels requires the operator API token")
		return
	}
	body, ok := decodeChannelBody(w, r)
	if !ok {
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
	s.log.Info("global alert channel created", "name", body.Name, "type", body.Type)
	writeJSON(w, http.StatusCreated, channelDTO{
		ID: id, Name: body.Name, Type: body.Type, URLMasked: maskURL(body.URL), Enabled: true,
	})
}

func (s *Server) handleUpdateChannel(w http.ResponseWriter, r *http.Request) {
	if !s.mayAdminister(r) {
		writeErr(w, http.StatusUnauthorized, "changing alert channels requires the operator API token")
		return
	}
	id, ok := pathID(w, r, "id", "channel id must be an integer")
	if !ok {
		return
	}
	body, ok := decodeChannelPatch(w, r)
	if !ok {
		return
	}
	s.updateChannel(w, r, id, nil, body)
}

func (s *Server) handleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	if !s.mayAdminister(r) {
		writeErr(w, http.StatusUnauthorized, "changing alert channels requires the operator API token")
		return
	}
	id, ok := pathID(w, r, "id", "channel id must be an integer")
	if !ok {
		return
	}
	s.deleteChannel(w, r, id, nil)
}

// updateChannel and deleteChannel carry the scope so a project route can never
// reach the global definition, whatever id it passes.
func (s *Server) updateChannel(w http.ResponseWriter, r *http.Request, id int64, scope *int64, body channelPatch) {
	found, err := s.db.UpdateChannel(r.Context(), id, scope, body.Name, body.URL, body.Enabled)
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

func (s *Server) deleteChannel(w http.ResponseWriter, r *http.Request, id int64, scope *int64) {
	found, err := s.db.DeleteChannel(r.Context(), id, scope)
	if err != nil {
		s.log.Error("delete channel failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "could not delete the channel")
		return
	}
	if !found {
		writeErr(w, http.StatusNotFound, "no such channel")
		return
	}
	s.log.Warn("alert channel deleted", "id", id, "project_scoped", scope != nil)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}

/* ---- test before save ---------------------------------------------------------
 * A webhook URL is a capability that either works or silently does not, and the
 * usual way to find out is an outage nobody was told about. Testing is therefore a
 * precondition for saving, not a nicety, and the test rides the live delivery path
 * so a pass means the real thing will pass.
 */

// trimError keeps an upstream's words but not its essays: a Go transport error or a
// 2 KB HTML error page would otherwise land whole in a toast.
func trimError(err error) string {
	msg := strings.TrimSpace(err.Error())
	const max = 300
	if len(msg) > max {
		return msg[:max] + "…"
	}
	return msg
}

// handleTestChannel delivers a probe to an unsaved channel config. It stores
// nothing: this endpoint is a question, and the answer is the status code.
func (s *Server) handleTestChannel(w http.ResponseWriter, r *http.Request) {
	if !s.mayAdminister(r) {
		writeErr(w, http.StatusUnauthorized, "testing alert channels requires the operator API token")
		return
	}
	var body struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "expected a JSON body with type and url")
		return
	}
	body.URL = strings.TrimSpace(body.URL)
	if body.Type == "" {
		body.Type = "teams"
	}
	// The same gate as create, so a URL that passes the test is a URL that can be saved.
	switch {
	case !allowedChannelTypes[body.Type]:
		writeErr(w, http.StatusBadRequest, "type must be teams, slack or webhook")
		return
	case !strings.HasPrefix(body.URL, "https://"):
		writeErr(w, http.StatusBadRequest, "a webhook URL starts with https://")
		return
	}

	// Bounded independently of the caller: the alerter's client allows 10s per
	// attempt, and a browser waiting on a hung webhook should still get an answer.
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	if err := s.alerter.TestChannel(ctx, body.Type, body.URL); err != nil {
		s.log.Warn("alert channel test failed", "type", body.Type, "err", err)
		// 502, not 400: the request was fine, the upstream was not — and the
		// upstream's own words are the useful part of the answer.
		writeErr(w, http.StatusBadGateway, trimError(err))
		return
	}
	s.log.Info("alert channel test passed", "type", body.Type)
	writeJSON(w, http.StatusOK, map[string]any{"tested": true})
}

func pathID(w http.ResponseWriter, r *http.Request, name, msg string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, msg)
		return 0, false
	}
	return id, true
}

/* ---- project-level alerts -----------------------------------------------------
 * A project's alerting is: which channels it routes to, and how patient it is
 * about bursts. Both live here, both scoped by the slug in the path.
 */

// project resolves the {slug} path value, writing the 404 itself. Every project
// alert route starts with this, so an unknown slug can never fall through to the
// global scope.
func (s *Server) project(w http.ResponseWriter, r *http.Request) (int64, bool) {
	slug := strings.TrimSpace(r.PathValue("slug"))
	id, err := s.db.ProjectIDBySlug(r.Context(), slug)
	if err != nil {
		s.log.Error("project lookup failed", "slug", slug, "err", err)
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return 0, false
	}
	if id == 0 {
		writeErr(w, http.StatusNotFound, "no such app")
		return 0, false
	}
	return id, true
}

// patienceChoices are the windows the UI offers, in seconds. Sentry calls this the
// action interval; the shape is the same — a ceiling on how often a channel is
// allowed to be interrupted.
var patienceChoices = []int{0, 60, 300, 600, 1800, 3600, 10800, 43200, 86400}

func (s *Server) handleProjectAlerts(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "sign in, or pass an ingest key")
		return
	}
	projectID, ok := s.project(w, r)
	if !ok {
		return
	}
	own, err := s.db.ListProjectChannels(r.Context(), projectID)
	if err != nil {
		s.log.Error("list project channels failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	globals, err := s.db.ListGlobalChannelsFor(r.Context(), projectID)
	if err != nil {
		s.log.Error("list global channels failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	patience, err := s.db.AlertPatience(r.Context(), projectID)
	if err != nil {
		s.log.Error("read alert patience failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"channels":         toChannelDTOs(own),
		"globals":          toChannelDTOs(globals),
		"patience_seconds": patience,
		"patience_choices": patienceChoices,
	})
}

func (s *Server) handleCreateProjectChannel(w http.ResponseWriter, r *http.Request) {
	if !s.mayAdminister(r) {
		writeErr(w, http.StatusUnauthorized, "changing alert channels requires the operator API token")
		return
	}
	projectID, ok := s.project(w, r)
	if !ok {
		return
	}
	body, ok := decodeChannelBody(w, r)
	if !ok {
		return
	}
	id, err := s.db.CreateProjectChannel(r.Context(), projectID, body.Name, body.Type, body.URL)
	if errors.Is(err, store.ErrChannelNameTaken) {
		writeErr(w, http.StatusConflict, "this app already has a channel with that name")
		return
	}
	if err != nil {
		s.log.Error("create project channel failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "could not create the channel")
		return
	}
	s.log.Info("project alert channel created",
		"project_id", projectID, "name", body.Name, "type", body.Type)
	writeJSON(w, http.StatusCreated, channelDTO{
		ID: id, Name: body.Name, Type: body.Type, URLMasked: maskURL(body.URL), Enabled: true,
	})
}

func (s *Server) handleUpdateProjectChannel(w http.ResponseWriter, r *http.Request) {
	if !s.mayAdminister(r) {
		writeErr(w, http.StatusUnauthorized, "changing alert channels requires the operator API token")
		return
	}
	projectID, ok := s.project(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id", "channel id must be an integer")
	if !ok {
		return
	}
	body, ok := decodeChannelPatch(w, r)
	if !ok {
		return
	}
	s.updateChannel(w, r, id, &projectID, body)
}

func (s *Server) handleDeleteProjectChannel(w http.ResponseWriter, r *http.Request) {
	if !s.mayAdminister(r) {
		writeErr(w, http.StatusUnauthorized, "changing alert channels requires the operator API token")
		return
	}
	projectID, ok := s.project(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id", "channel id must be an integer")
	if !ok {
		return
	}
	s.deleteChannel(w, r, id, &projectID)
}

// handleImportChannel subscribes the project to a global channel. Import is a
// reference, not a copy: the URL keeps living in the catalogue, so rotating it
// there rotates it for every project that imported it.
func (s *Server) handleImportChannel(w http.ResponseWriter, r *http.Request) {
	if !s.mayAdminister(r) {
		writeErr(w, http.StatusUnauthorized, "changing alert channels requires the operator API token")
		return
	}
	projectID, ok := s.project(w, r)
	if !ok {
		return
	}
	var body struct {
		ChannelID  int64   `json:"channel_id"`
		ChannelIDs []int64 `json:"channel_ids"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "expected a JSON body with channel_id or channel_ids")
		return
	}
	ids := body.ChannelIDs
	if body.ChannelID != 0 {
		ids = append(ids, body.ChannelID)
	}
	if len(ids) == 0 {
		writeErr(w, http.StatusBadRequest, "name at least one channel_id to import")
		return
	}
	if len(ids) > 64 {
		writeErr(w, http.StatusBadRequest, "import at most 64 channels at a time")
		return
	}
	imported := make([]int64, 0, len(ids))
	for _, id := range ids {
		ok, err := s.db.ImportChannel(r.Context(), projectID, id)
		if err != nil {
			s.log.Error("import channel failed", "project_id", projectID, "channel_id", id, "err", err)
			writeErr(w, http.StatusServiceUnavailable, "could not import the channel")
			return
		}
		if !ok {
			writeErr(w, http.StatusNotFound, "no such global channel")
			return
		}
		imported = append(imported, id)
	}
	s.log.Info("global channels imported", "project_id", projectID, "channels", imported)
	writeJSON(w, http.StatusOK, map[string]any{"imported": imported})
}

func (s *Server) handleUnimportChannel(w http.ResponseWriter, r *http.Request) {
	if !s.mayAdminister(r) {
		writeErr(w, http.StatusUnauthorized, "changing alert channels requires the operator API token")
		return
	}
	projectID, ok := s.project(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id", "channel id must be an integer")
	if !ok {
		return
	}
	found, err := s.db.UnimportChannel(r.Context(), projectID, id)
	if err != nil {
		s.log.Error("unimport channel failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "could not remove the import")
		return
	}
	if !found {
		writeErr(w, http.StatusNotFound, "this app has not imported that channel")
		return
	}
	s.log.Info("global channel un-imported", "project_id", projectID, "channel_id", id)
	writeJSON(w, http.StatusOK, map[string]any{"unimported": id})
}

// handleSetPatience changes the burst window. Only the offered choices are
// accepted so the stored value always matches a label the UI can render.
func (s *Server) handleSetPatience(w http.ResponseWriter, r *http.Request) {
	if !s.mayAdminister(r) {
		writeErr(w, http.StatusUnauthorized, "changing alert settings requires the operator API token")
		return
	}
	projectID, ok := s.project(w, r)
	if !ok {
		return
	}
	var body struct {
		Seconds int `json:"seconds"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "expected a JSON body with seconds")
		return
	}
	if !slices.Contains(patienceChoices, body.Seconds) {
		writeErr(w, http.StatusBadRequest, "seconds must be one of the offered patience windows")
		return
	}
	found, err := s.db.SetAlertPatience(r.Context(), projectID, body.Seconds)
	if err != nil {
		s.log.Error("set alert patience failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "could not save the setting")
		return
	}
	if !found {
		writeErr(w, http.StatusNotFound, "no such app")
		return
	}
	s.log.Info("alert patience changed", "project_id", projectID, "seconds", body.Seconds)
	writeJSON(w, http.StatusOK, map[string]any{"patience_seconds": body.Seconds})
}
