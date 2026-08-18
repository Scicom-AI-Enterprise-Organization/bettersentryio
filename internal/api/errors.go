package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Scicom-AI-Enterprise-Organization/bettersentryio/internal/events"
)

// maxEventBytes caps a single event. Stacktraces with source context are a few KB; a
// megabyte means something is wrong, and ingest must not be a memory amplifier.
const maxEventBytes = 512 << 10

// handleIngestError takes one error event from an SDK.
//
// Authenticated with the project's ingest key, exactly like a beat: reporting your own
// crashes is the same privilege as reporting your own heartbeats. It answers 202 as soon
// as the event is stored — the SDK must never wait on us, and has nothing to do with a
// richer reply.
func (s *Server) handleIngestError(w http.ResponseWriter, r *http.Request) {
	key := presentedKey(r)
	if key == "" {
		writeErr(w, http.StatusUnauthorized, "missing ingest key (X-BSIO-Key header or ?key=)")
		return
	}
	projectID, err := s.db.ProjectIDForKey(r.Context(), key)
	if err != nil {
		s.log.Error("key lookup failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if projectID == 0 {
		writeErr(w, http.StatusForbidden, "unknown ingest key")
		return
	}

	var e events.Event
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxEventBytes))
	if err := dec.Decode(&e); err != nil {
		writeErr(w, http.StatusBadRequest, "expected a JSON event: "+err.Error())
		return
	}
	if e.Exception == nil && e.Message == "" {
		writeErr(w, http.StatusBadRequest, "event needs an exception or a message")
		return
	}

	res, err := s.events.Ingest(r.Context(), projectID, &e)
	if err != nil {
		s.log.Error("ingest error event failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "could not record the event")
		return
	}
	if res.IsNew {
		s.log.Info("new issue", "issue", res.IssueID, "culprit", res.Culprit)
	}
	s.notifyIssue(r.Context(), projectID, res)
	writeJSON(w, http.StatusAccepted, res)
}

func (s *Server) handleIssues(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "sign in, or pass an ingest key")
		return
	}
	slug := r.URL.Query().Get("project")
	if slug == "" {
		writeErr(w, http.StatusBadRequest, "project is required")
		return
	}
	includeResolved := r.URL.Query().Get("resolved") == "true"
	includeArchived := r.URL.Query().Get("archived") == "true"
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	// ?tag=key:value, repeatable — filters on the server-derived issue tags.
	tagFilters := map[string]string{}
	for _, raw := range r.URL.Query()["tag"] {
		if k, v, ok := strings.Cut(raw, ":"); ok && k != "" {
			tagFilters[k] = v
		}
	}

	list, err := s.events.Issues(r.Context(), slug, includeResolved, includeArchived, limit, tagFilters)
	if err != nil {
		s.log.Error("list issues failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	counts, err := s.events.Counts(r.Context(), slug)
	if err != nil {
		s.log.Error("issue counts failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"issues": list, "counts": counts})
}

func (s *Server) handleIssueDetail(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "sign in, or pass an ingest key")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "issue id must be an integer")
		return
	}
	detail, ok, err := s.events.Issue(r.Context(), id)
	if err != nil {
		s.log.Error("issue detail failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "no such issue")
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// handleResolveIssue is operator-only: an ingest key may report crashes, not decide
// which of them count as fixed.
func (s *Server) handleResolveIssue(w http.ResponseWriter, r *http.Request) {
	if !s.mayAdminister(r) {
		writeErr(w, http.StatusUnauthorized, "resolving an issue requires the operator API token")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "issue id must be an integer")
		return
	}
	resolved := r.URL.Query().Get("resolved") != "false"
	if err := s.events.SetResolved(r.Context(), id, resolved); err != nil {
		s.log.Error("resolve issue failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"issue": id, "resolved": resolved})
}

// handleArchiveIssue archives (or un-archives) an issue. Modes: "forever",
// "for" (+hours), "recur" (until it occurs again), "off".
func (s *Server) handleArchiveIssue(w http.ResponseWriter, r *http.Request) {
	if !s.mayAdminister(r) {
		writeErr(w, http.StatusUnauthorized, "archiving an issue requires the operator API token")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "issue id must be an integer")
		return
	}
	var body struct {
		Mode  string  `json:"mode"`
		Hours float64 `json:"hours"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "expected a JSON body with a mode")
		return
	}
	var until *time.Time
	archived, recur := true, false
	switch body.Mode {
	case "forever":
	case "recur":
		recur = true
	case "for":
		if body.Hours <= 0 || body.Hours > 24*365 {
			writeErr(w, http.StatusBadRequest, "hours must be between 0 and 8760")
			return
		}
		t := time.Now().Add(time.Duration(body.Hours * float64(time.Hour)))
		until = &t
	case "off":
		archived = false
	default:
		writeErr(w, http.StatusBadRequest, "mode must be forever, for, recur or off")
		return
	}
	if err := s.events.SetArchived(r.Context(), id, archived, until, recur); err != nil {
		s.log.Error("archive issue failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "could not update the issue")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"issue": id, "archived": archived, "mode": body.Mode})
}

var validPriorities = map[string]bool{"": true, "high": true, "med": true, "low": true}

func (s *Server) handleIssuePriority(w http.ResponseWriter, r *http.Request) {
	if !s.mayAdminister(r) {
		writeErr(w, http.StatusUnauthorized, "setting priority requires the operator API token")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "issue id must be an integer")
		return
	}
	var body struct {
		Priority string `json:"priority"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil || !validPriorities[body.Priority] {
		writeErr(w, http.StatusBadRequest, "priority must be high, med, low or empty")
		return
	}
	if err := s.events.SetPriority(r.Context(), id, body.Priority); err != nil {
		s.log.Error("set priority failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "could not update the issue")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"issue": id, "priority": body.Priority})
}

// handleDeleteIssue removes an issue and its events. Operator-only, like
// resolving: an ingest key reports crashes, it does not erase history.
func (s *Server) handleDeleteIssue(w http.ResponseWriter, r *http.Request) {
	if !s.mayAdminister(r) {
		writeErr(w, http.StatusUnauthorized, "deleting an issue requires the operator API token")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "issue id must be an integer")
		return
	}
	found, err := s.events.DeleteIssue(r.Context(), id)
	if err != nil {
		s.log.Error("delete issue failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "could not delete the issue")
		return
	}
	if !found {
		writeErr(w, http.StatusNotFound, "no such issue")
		return
	}
	s.log.Warn("issue deleted", "issue", id)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}

// handleIssueEvent returns one specific stored event of an issue, so the UI
// can step through occurrences instead of only showing the latest.
func (s *Server) handleIssueEvent(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "sign in, or pass an ingest key")
		return
	}
	id, err1 := strconv.ParseInt(r.PathValue("id"), 10, 64)
	eventID, err2 := strconv.ParseInt(r.PathValue("eventID"), 10, 64)
	if err1 != nil || err2 != nil {
		writeErr(w, http.StatusBadRequest, "ids must be integers")
		return
	}
	payload, ok, err := s.events.EventByID(r.Context(), id, eventID)
	if err != nil {
		s.log.Error("event lookup failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "no such event in this issue")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": eventID, "payload": json.RawMessage(payload)})
}
