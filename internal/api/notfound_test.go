package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// requireSession stands in for the UI's catch-all, which authenticates by session
// cookie only. Before the /api/0/ catch-all existed, every unrouted API path landed
// here and answered 401 to a caller holding a valid token.
func requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"authentication required"}`))
	})
}

type uiStub struct{}

func (uiStub) Routes(mux *http.ServeMux) { mux.Handle("/", requireSession(nil)) }

func notFoundServer() *Server {
	return &Server{
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		apiToken: "operator-token",
	}
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("Authorization", "Bearer operator-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestUnroutedAPIPathIs404NotUnauthorized(t *testing.T) {
	h := notFoundServer().Handler(uiStub{})
	// A trailing slash is all it takes: /api/0/issues/{id} is routed, this is not.
	for _, path := range []string{
		"/api/0/issues/",
		"/api/0/auth/",
		"/api/0/projects/bettersentryio/default/issues/",
	} {
		w := get(t, h, path)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: got %d, want 404 -- a valid token must not be told it is invalid", path, w.Code)
		}
		var body map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: body is not JSON: %v", path, err)
		}
		if body["error"] == "authentication required" {
			t.Errorf("%s: still answering as an auth failure", path)
		}
	}
}

func TestUnroutedSentryPathAnswersInSentrysShape(t *testing.T) {
	// The Grafana datasource shows `detail` verbatim and ignores `error`, so a 404
	// under the Sentry Web API has to speak Sentry's error shape to say anything.
	w := get(t, notFoundServer().Handler(uiStub{}), "/api/0/organizations/bettersentryio/nope/")
	if w.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", w.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if body["detail"] == "" {
		t.Errorf("no `detail` for the datasource to show: %s", w.Body.String())
	}
}

func TestRoutedPathsAreStillReachable(t *testing.T) {
	// The catch-all is a prefix pattern; the registered ones are more specific and
	// must keep winning, or this fix would swallow the whole API.
	w := get(t, notFoundServer().Handler(uiStub{}), "/api/0/organizations")
	if w.Code == http.StatusNotFound {
		t.Errorf("registered route lost to the catch-all")
	}
}
