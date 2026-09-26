package web

import (
	"net/http"
	"strings"

	"andon/internal/services/system"
)

const embedPrefix = "/embed/"

// csp builds the Content-Security-Policy: everything same-origin, iframe
// widgets only from admin-listed origins, and only /embed/ pages may be
// framed by other sites.
func (d Deps) csp(path string) string {
	frames := strings.Join(system.IframeOrigins(d.DB), " ")
	if frames == "" {
		frames = "'none'"
	}
	ancestors := "'self'"
	if strings.HasPrefix(path, embedPrefix) {
		ancestors = "*"
	}
	return "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; " +
		"frame-src " + frames + "; frame-ancestors " + ancestors + "; base-uri 'self'; form-action 'self'"
}

// Secure adds the security headers to every response.
func (d Deps) Secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", d.csp(r.URL.Path))
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}
