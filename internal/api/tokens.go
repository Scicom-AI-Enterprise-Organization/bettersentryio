// Auth tokens: what you hand a dashboard.
//
// The read API previously accepted two credentials, and neither was the right thing to
// give Grafana. The operator token can delete apps and lives in an env var, so it can
// be neither scoped down nor revoked without a redeploy; an ingest key is minted for
// writing events and is embedded in client code. A token you can name, hand out, see
// the last use of, and revoke on its own is the missing third thing — and it is what
// every Sentry client calls an auth token.
package api

import (
	"encoding/json"
	"net/http"
	"strconv"
)

func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	if !s.mayAdminister(r) {
		writeErr(w, http.StatusForbidden, "the operator token or a signed-in session is required")
		return
	}
	tokens, err := s.db.ListAPITokens(r.Context())
	if err != nil {
		s.log.Error("list tokens failed", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": tokens})
}

// handleCreateToken mints one. The plaintext is in this response and nowhere else,
// ever again — the caller is expected to show it once and say so.
func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	if !s.mayAdminister(r) {
		writeErr(w, http.StatusForbidden, "the operator token or a signed-in session is required")
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "expected a JSON body with a name")
		return
	}
	plaintext, token, err := s.db.CreateAPIToken(r.Context(), body.Name)
	if err != nil {
		// A name that fails validation is the caller's mistake, not an outage; the
		// store's message already says which rule was broken.
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.log.Info("api token created", "id", token.ID, "name", token.Name, "prefix", token.Prefix)
	writeJSON(w, http.StatusCreated, map[string]any{"token": token, "secret": plaintext})
}

func (s *Server) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	if !s.mayAdminister(r) {
		writeErr(w, http.StatusForbidden, "the operator token or a signed-in session is required")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "token id must be an integer")
		return
	}
	found, err := s.db.RevokeAPIToken(r.Context(), id)
	if err != nil {
		s.log.Error("revoke token failed", "id", id, "err", err)
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if !found {
		writeErr(w, http.StatusNotFound, "no such token")
		return
	}
	s.log.Info("api token revoked", "id", id)
	writeJSON(w, http.StatusOK, map[string]any{"revoked": id})
}
