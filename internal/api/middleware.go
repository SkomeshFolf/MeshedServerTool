package api

import (
	"log"
	"net/http"
	"runtime/debug"
	"strings"
)

// chainMiddleware composes a slice of middlewares around a handler.
// Each middleware in `mws` wraps the handler produced by the next.
// Order: mws[0]( mws[1]( ... (handler) ... )).
// So mws[0] is the OUTERMOST (runs first on the way in, last on the way out).
func chainMiddleware(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// panicRecoverMiddleware catches any panic in the wrapped handler and
// returns 500 instead of letting it crash the process. (MED-1)
//
// Logged with stack trace so we can fix the underlying panic. This is
// deliberately outermost — even if a later middleware panics (it
// shouldn't, but defense in depth) the server keeps serving.
func panicRecoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("PANIC in HTTP handler: %v\n%s\n  url=%s method=%s remote=%s",
					rec, debug.Stack(), r.URL.Path, r.Method, r.RemoteAddr)
				// Best-effort: only write headers if nothing has been
				// written yet. http.Error does this check itself.
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// securityHeadersMiddleware adds standard security headers. (HIGH-2)
// When `enabled` is false it returns a pass-through — useful for tests.
//
// The CSP is intentionally strict: same-origin only. The frontend is
// served from the same origin as the API, so no external scripts are
// needed. If you front the API with a CDN or a different-origin
// frontend, you'll need to relax script-src.
func securityHeadersMiddleware(enabled bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if !enabled {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			// HSTS: 2 years, include subdomains. Only meaningful over HTTPS,
			// but harmless over HTTP (browsers ignore it).
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
			// Prevent MIME-sniffing.
			h.Set("X-Content-Type-Options", "nosniff")
			// Don't allow embedding in iframes (clickjacking).
			h.Set("X-Frame-Options", "DENY")
			// Don't leak the URL as a Referer.
			h.Set("Referrer-Policy", "no-referrer")
			// Strict CSP. Default-src 'self' so any future addition is
			// explicit. unsafe-inline for style is allowed because the
			// React build inlines critical CSS — 'self' would block it.
			h.Set("Content-Security-Policy",
				"default-src 'self'; "+
					"script-src 'self'; "+
					"style-src 'self' 'unsafe-inline'; "+
					"img-src 'self' data:; "+
					"connect-src 'self' ws: wss:; "+
					"frame-ancestors 'none'; "+
					"base-uri 'self'")
			// Disable powerful features the SPA doesn't use.
			h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
			next.ServeHTTP(w, r)
		})
	}
}

// corsMiddleware handles Access-Control-Allow-* for cross-origin
// browser clients. (HIGH-2)
//
// Behavior:
//   - allowedOrigins == nil/empty → no CORS headers, browser blocks (safe default)
//   - allowedOrigins contains "*" → reflect request origin (insecure, dev only)
//   - allowedOrigins contains specific origin → echo it back if request matches
//
// Preflight OPTIONS requests are handled in-place. Other requests just
// get the headers set and pass through.
func corsMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	wildcard := false
	allowSet := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		if o == "*" {
			wildcard = true
			continue
		}
		allowSet[strings.TrimSpace(o)] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				// Not a cross-origin request. No CORS headers needed.
				next.ServeHTTP(w, r)
				return
			}
			// Decide whether to allow this origin.
			allow := false
			echo := ""
			if wildcard {
				allow = true
				echo = "*"
			} else if _, ok := allowSet[origin]; ok {
				allow = true
				echo = origin
			}
			if !allow {
				next.ServeHTTP(w, r)
				return
			}
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", echo)
			h.Set("Vary", "Origin")
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			h.Set("Access-Control-Max-Age", "86400")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
