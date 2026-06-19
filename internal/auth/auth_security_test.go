package auth

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

// resetCookieSecure resets the package-level override + hook between
// tests so they don't leak state into each other. Holds the package
// mutex for both the snapshot and the restore so a parallel test
// can't observe partial state.
func resetCookieSecure(t *testing.T) {
	t.Helper()
	cookieSecureMu.Lock()
	prevOverride := cookieSecureOverride
	prevHook := cookieSecureHook
	cookieSecureOverride = nil
	cookieSecureHook = nil
	cookieSecureMu.Unlock()
	t.Cleanup(func() {
		cookieSecureMu.Lock()
		cookieSecureOverride = prevOverride
		cookieSecureHook = prevHook
		cookieSecureMu.Unlock()
	})
}

// TestSetSessionCookie_SecureDefaultsToTLS verifies the default
// behavior (no override, no hook): Secure matches r.TLS != nil. (HIGH-1)
//
// NOT t.Parallel() — tests share the cookieSecureOverride/cookieSecureHook
// package globals. The mutex in cookieSecureMu makes the writes/reads
// race-free, but test B reading while test A's cleanup hasn't run yet
// would observe stale state. Running sequentially avoids that.
func TestSetSessionCookie_SecureDefaultsToTLS(t *testing.T) {
	resetCookieSecure(t)
	cases := []struct {
		name       string
		tls        bool
		wantSecure bool
	}{
		{"plain_http_no_secure", false, false},
		{"https_secure", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}
			rr := httptest.NewRecorder()
			SetSessionCookie(rr, r, "tok")
			c := rr.Result().Cookies()
			if len(c) == 0 {
				t.Fatal("no cookie set")
			}
			if c[0].Secure != tc.wantSecure {
				t.Errorf("Secure = %v, want %v", c[0].Secure, tc.wantSecure)
			}
		})
	}
}

// TestSetSessionCookie_SecureOverrideForcedOn verifies the production
// flag: --cookie-secure=always. (HIGH-1)
func TestSetSessionCookie_SecureOverrideForcedOn(t *testing.T) {
	resetCookieSecure(t)
	tr := true
	SetCookieSecureOverride(&tr)
	r := httptest.NewRequest("GET", "/", nil) // plain HTTP
	rr := httptest.NewRecorder()
	SetSessionCookie(rr, r, "tok")
	c := rr.Result().Cookies()
	if !c[0].Secure {
		t.Error("override=&true: Secure should be on even over plain HTTP")
	}
}

// TestSetSessionCookie_SecureOverrideForcedOff verifies the local
// testing flag: --cookie-secure=never.
func TestSetSessionCookie_SecureOverrideForcedOff(t *testing.T) {
	resetCookieSecure(t)
	fa := false
	SetCookieSecureOverride(&fa)
	r := httptest.NewRequest("GET", "/", nil)
	r.TLS = &tls.ConnectionState{}
	rr := httptest.NewRecorder()
	SetSessionCookie(rr, r, "tok")
	c := rr.Result().Cookies()
	if c[0].Secure {
		t.Error("override=&false: Secure should be off even over HTTPS")
	}
}

// TestSetSessionCookie_SecureHookXFF verifies the hook path: when no
// override is set, the hook decides. (HIGH-1)
func TestSetSessionCookie_SecureHookXFF(t *testing.T) {
	resetCookieSecure(t)
	WithCookieSecureHook(func(r *http.Request) bool {
		return r.Header.Get("X-Force-Secure") == "yes"
	})
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Force-Secure", "yes")
	rr := httptest.NewRecorder()
	SetSessionCookie(rr, r, "tok")
	c := rr.Result().Cookies()
	if !c[0].Secure {
		t.Error("hook returned true: Secure should be on")
	}
}

// TestClearSessionCookie_FollowsSameRule verifies ClearSessionCookie
// uses the same logic as SetSessionCookie.
func TestClearSessionCookie_FollowsSameRule(t *testing.T) {
	resetCookieSecure(t)
	tr := true
	SetCookieSecureOverride(&tr)
	r := httptest.NewRequest("GET", "/", nil) // plain HTTP
	rr := httptest.NewRecorder()
	ClearSessionCookie(rr, r)
	c := rr.Result().Cookies()
	if !c[0].Secure {
		t.Error("ClearSessionCookie: Secure should follow override")
	}
	if c[0].MaxAge != -1 {
		t.Errorf("ClearSessionCookie: MaxAge = %d, want -1", c[0].MaxAge)
	}
}
