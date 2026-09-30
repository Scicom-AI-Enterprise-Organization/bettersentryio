package api

import "net/http"

// engineCSP fits the only HTML the engine serves, the legacy operator UI in
// internal/web: its CSS and reload script come from /static, the favicon is a data:
// URI, and the sparkline bars size themselves with inline style attributes. Every
// other response is JSON or text, where the policy is inert apart from
// frame-ancestors.
const engineCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; object-src 'none'; base-uri 'self'; form-action 'self'; " +
	"frame-ancestors 'none'"

// securityHeaders stamps the browser hardening headers on every engine response
// (VAPT SAST: missing HSTS/CSP on the client download, framing of the legacy
// dashboard). They are set before the handler runs so error paths — http.Error,
// the 401 from the UI's Require — carry them too. HSTS is ignored by browsers over
// plain http, so a local :9090 is unaffected.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", engineCSP)
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}
