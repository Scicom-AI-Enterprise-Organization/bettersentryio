package api

// Read side of the SDK-coverage ingest: release health rows for the Releases
// view, and attachment listing/download for the event page.

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// handleReleases is GET /api/0/releases?project=<slug>&days=<n>.
func (s *Server) handleReleases(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "authentication required")
		return
	}
	slug := r.URL.Query().Get("project")
	if slug == "" {
		writeErr(w, http.StatusBadRequest, "project is required")
		return
	}
	days := 30
	if d, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && d > 0 && d <= 90 {
		days = d
	}
	rows, err := s.events.ReleaseHealth(r.Context(), slug, time.Now().Add(-time.Duration(days)*24*time.Hour))
	if err != nil {
		s.log.Error("release health query failed", "err", err)
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"releases": rows, "days": days})
}

// handleEventAttachments is GET /api/0/events/{uuid}/attachments?project=<slug>.
func (s *Server) handleEventAttachments(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "authentication required")
		return
	}
	slug := r.URL.Query().Get("project")
	if slug == "" {
		writeErr(w, http.StatusBadRequest, "project is required")
		return
	}
	list, err := s.events.Attachments(r.Context(), slug, r.PathValue("uuid"))
	if err != nil {
		s.log.Error("attachment list failed", "err", err)
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"attachments": list})
}

// handleAttachmentDownload is GET /api/0/attachments/{id}: the bytes, served
// with a Content-Disposition so a browser saves rather than renders them —
// attachments are SDK-supplied and must never execute in this origin.
func (s *Server) handleAttachmentDownload(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "authentication required")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusNotFound, "no such attachment")
		return
	}
	filename, contentType, data, ok, err := s.events.AttachmentData(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "no such attachment")
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}
