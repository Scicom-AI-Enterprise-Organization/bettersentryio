// Package clients embeds the SDK source files so a running engine can serve them.
//
// The setup page tells you to curl the Python client straight off the engine you are
// about to report to. That only works if the binary carries it, which also keeps the
// served client and the engine at the same version — there is no separate artifact to
// publish or forget to publish.
package clients

import (
	"embed"
	"net/http"
	"strings"
)

//go:embed python/bettersentryio.py
var files embed.FS

// Routes serves the SDK sources read-only. Deliberately unauthenticated: the client
// is public source code with no secrets in it, and requiring a key to download the
// thing you need in order to use a key is a chicken-and-egg problem.
func Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /clients/python/bettersentryio.py", func(w http.ResponseWriter, r *http.Request) {
		body, err := files.ReadFile("python/bettersentryio.py")
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/x-python; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		if strings.Contains(r.URL.RawQuery, "download") {
			w.Header().Set("Content-Disposition", `attachment; filename="bettersentryio.py"`)
		}
		_, _ = w.Write(body)
	})
}
