package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPanicRecoverMiddleware confirms that a handler panic is caught
// and returned as 500 instead of killing the process. (MED-1)
func TestPanicRecoverMiddleware(t *testing.T) {
	t.Parallel()
	panicker := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	h := panicRecoverMiddleware(panicker)

	rr := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/panic", nil)
	h.ServeHTTP(rr, r)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("got status %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "internal server error") {
		t.Errorf("body %q should mention 'internal server error'", rr.Body.String())
	}
}

// TestPanicRecoverMiddleware_PassesThrough verifies normal handlers
// are unaffected.
func TestPanicRecoverMiddleware_PassesThrough(t *testing.T) {
	t.Parallel()
	normal := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("ok"))
	})
	h := panicRecoverMiddleware(normal)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusTeapot || rr.Body.String() != "ok" {
		t.Errorf("got %d %q, want 418 'ok'", rr.Code, rr.Body.String())
	}
}

// TestSecurityHeadersMiddleware_Disabled verifies that when the flag is
// off, no security headers are added (useful for tests/debug).
func TestSecurityHeadersMiddleware_Disabled(t *testing.T) {
	t.Parallel()
	h := securityHeadersMiddleware(false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The handler inspects the response headers itself to confirm
		// the middleware didn't touch them.
		if got := w.Header().Get("Strict-Transport-Security"); got != "" {
			t.Errorf("HSTS should not be set when disabled, got %q", got)
		}
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
}

// TestSecurityHeadersMiddleware_Enabled verifies all headers are set
// when the flag is on. (HIGH-2)
func TestSecurityHeadersMiddleware_Enabled(t *testing.T) {
	t.Parallel()
	h := securityHeadersMiddleware(true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, hdr := range []string{
			"Strict-Transport-Security",
			"X-Content-Type-Options",
			"X-Frame-Options",
			"Referrer-Policy",
			"Content-Security-Policy",
			"Permissions-Policy",
		} {
			if got := w.Header().Get(hdr); got == "" {
				t.Errorf("%s header missing", hdr)
			}
		}
		// Specific value assertions.
		if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
		}
		if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
			t.Errorf("X-Frame-Options = %q, want DENY", got)
		}
		if got := w.Header().Get("Referrer-Policy"); got != "no-referrer" {
			t.Errorf("Referrer-Policy = %q, want no-referrer", got)
		}
		if got := w.Header().Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'self'") {
			t.Errorf("CSP = %q, want default-src 'self'", got)
		}
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
}

// TestCorsMiddleware_NoOrigin verifies that requests without an Origin
// header (same-origin) pass through with no CORS headers set.
func TestCorsMiddleware_NoOrigin(t *testing.T) {
	t.Parallel()
	h := corsMiddleware([]string{"https://allowed.example"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("AC-Allow-Origin should be empty, got %q", got)
		}
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
}

// TestCorsMiddleware_AllowedOrigin verifies a permitted origin gets
// the headers echoed back. (HIGH-2)
func TestCorsMiddleware_AllowedOrigin(t *testing.T) {
	t.Parallel()
	h := corsMiddleware([]string{"https://allowed.example"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://allowed.example" {
			t.Errorf("AC-Allow-Origin = %q, want https://allowed.example", got)
		}
		if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
			t.Errorf("AC-Allow-Credentials = %q, want true", got)
		}
	}))
	rr := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Origin", "https://allowed.example")
	h.ServeHTTP(rr, r)
}

// TestCorsMiddleware_DeniedOrigin verifies a non-permitted origin gets
// no CORS headers (browser will block).
func TestCorsMiddleware_DeniedOrigin(t *testing.T) {
	t.Parallel()
	h := corsMiddleware([]string{"https://allowed.example"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("AC-Allow-Origin should be empty for denied origin, got %q", got)
		}
	}))
	rr := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Origin", "https://evil.example")
	h.ServeHTTP(rr, r)
}

// TestCorsMiddleware_Wildcard verifies "*" reflects any origin. (dev only)
func TestCorsMiddleware_Wildcard(t *testing.T) {
	t.Parallel()
	h := corsMiddleware([]string{"*"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
			t.Errorf("AC-Allow-Origin = %q, want *", got)
		}
	}))
	rr := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Origin", "https://any.example")
	h.ServeHTTP(rr, r)
}

// TestCorsMiddleware_Preflight verifies OPTIONS preflight is answered
// in-place without invoking the inner handler.
func TestCorsMiddleware_Preflight(t *testing.T) {
	t.Parallel()
	innerCalled := false
	h := corsMiddleware([]string{"https://allowed.example"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		innerCalled = true
	}))
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodOptions, "/", nil)
	r.Header.Set("Origin", "https://allowed.example")
	r.Header.Set("Access-Control-Request-Method", "POST")
	h.ServeHTTP(rr, r)
	if innerCalled {
		t.Error("inner handler should not be called for preflight OPTIONS")
	}
	if rr.Code != http.StatusNoContent {
		t.Errorf("preflight: got %d, want 204", rr.Code)
	}
}

// TestChainMiddleware_Order verifies that the OUTERMOST middleware
// (first in the slice) wraps the others, so it sees the response last.
func TestChainMiddleware_Order(t *testing.T) {
	t.Parallel()
	var order []string
	mw := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, "in:"+name)
				next.ServeHTTP(w, r)
				order = append(order, "out:"+name)
			})
		}
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "inner")
	})
	chained := chainMiddleware(inner, mw("A"), mw("B"), mw("C"))
	chained.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	want := []string{"in:A", "in:B", "in:C", "inner", "out:C", "out:B", "out:A"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("order[%d] = %q, want %q", i, order[i], want[i])
		}
	}
}
