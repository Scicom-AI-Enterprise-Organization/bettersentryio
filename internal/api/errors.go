package api

import (
	"encoding/json"
	"net/http"
	"strconv"

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
		s.notifyNewIssue(r.Context(), projectID, res)
	}
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
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	list, err := s.events.Issues(r.Context(), slug, includeResolved, limit)
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
