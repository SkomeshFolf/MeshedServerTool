package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Skomesh/MeshedServerTool/internal/auth"
	"github.com/Skomesh/MeshedServerTool/internal/chat"
	"github.com/Skomesh/MeshedServerTool/internal/hub"
	"github.com/Skomesh/MeshedServerTool/internal/server"
	"github.com/Skomesh/MeshedServerTool/internal/storage"
)

// newTestAuthEnv wires up a v1AuthDeps plus a fresh in-memory DB.
// Returns the deps, the store (for direct row checks), and the
// auth service (for token-issuing helpers in middleware tests).
func newTestAuthEnv(t *testing.T) (*v1AuthDeps, *storage.Store, *auth.Service) {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "auth-test.db") +
		"?mode=memory&cache=shared&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	store, err := storage.OpenFromDSN(dsn)
	if err != nil {
		t.Fatalf("storage.OpenFromDSN: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	svc := auth.NewService(store)
	deps := &v1AuthDeps{svc: svc, store: store}
	return deps, store, svc
}

// Note: v1ServerDeps.ServeHTTP expects to be reached AFTER the
// router has stripped /api/v1/servers. The test path is the
// post-strip remainder. "" is the bare collection; "name" is a
// single server; "name/action" is a sub-resource.
func jsonRequest(method, target string, body any) *http.Request {
	var r *http.Request
	if body == nil {
		r = httptest.NewRequest(method, target, nil)
	} else {
		b, _ := json.Marshal(body)
		r = httptest.NewRequest(method, target, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
	}
	// httptest.NewRequest sets RemoteAddr to "192.0.2.1:1234" by
	// default; that's fine for our purposes.
	return r
}

// TestHandleBootstrap_SuccessAndSelfDisables walks the full bootstrap
// contract: empty users → 201, second bootstrap → 403. The
// self-disable is the security-critical path.
func TestHandleBootstrap_SuccessAndSelfDisables(t *testing.T) {
	t.Parallel()
	deps, store, _ := newTestAuthEnv(t)

	// 1. First call succeeds.
	rr := httptest.NewRecorder()
	deps.handleBootstrap(rr, jsonRequest("POST", "/api/v1/auth/bootstrap", map[string]string{
		"username": "admin", "password": "hunter22",
	}))
	if rr.Code != http.StatusCreated {
		t.Fatalf("first bootstrap: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	// Response should include the role.
	var firstResp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &firstResp)
	if firstResp["role"] != "admin" {
		t.Errorf("first bootstrap role = %v, want admin", firstResp["role"])
	}
	// Session cookie should be set.
	cookies := rr.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected a session cookie after bootstrap, got none")
	}
	if cookies[0].Name != auth.SessionCookieName {
		t.Errorf("cookie name = %q, want %q", cookies[0].Name, auth.SessionCookieName)
	}
	if !cookies[0].HttpOnly {
		t.Error("session cookie should be HttpOnly")
	}
	if cookies[0].SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie SameSite = %v, want Lax", cookies[0].SameSite)
	}
	if cookies[0].MaxAge <= 0 {
		t.Errorf("cookie MaxAge = %d, want > 0", cookies[0].MaxAge)
	}
	// Session row should be in the DB.
	row := store.DB().QueryRow("SELECT COUNT(*) FROM sessions")
	var n int
	_ = row.Scan(&n)
	if n != 1 {
		t.Errorf("expected 1 session row after bootstrap, got %d", n)
	}

	// 2. Second call must be rejected.
	rr2 := httptest.NewRecorder()
	deps.handleBootstrap(rr2, jsonRequest("POST", "/api/v1/auth/bootstrap", map[string]string{
		"username": "second", "password": "hunter22",
	}))
	if rr2.Code != http.StatusForbidden {
		t.Errorf("second bootstrap: got %d, want 403; body=%s", rr2.Code, rr2.Body.String())
	}
}

// TestHandleBootstrap_RejectsBadUsername walks the username validation
// rules. The exact policy is in validateUsername; we just check the
// handler maps it to 400 and the rejection doesn't write a user.
func TestHandleBootstrap_RejectsBadUsername(t *testing.T) {
	t.Parallel()
	deps, store, _ := newTestAuthEnv(t)

	bad := []struct {
		name     string
		username string
	}{
		{"too_short", "ab"},
		{"too_long", strings.Repeat("a", 33)},
		{"bad_chars", "ad min"},
		{"bad_punct", "admin!"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			deps.handleBootstrap(rr, jsonRequest("POST", "/api/v1/auth/bootstrap", map[string]string{
				"username": tc.username, "password": "hunter22",
			}))
			if rr.Code != http.StatusBadRequest {
				t.Errorf("got %d, want 400; body=%s", rr.Code, rr.Body.String())
			}
		})
	}
	// Verify no user was created.
	var n int
	_ = store.DB().QueryRow("SELECT COUNT(*) FROM users").Scan(&n)
	if n != 0 {
		t.Errorf("expected 0 users, got %d", n)
	}
}

// TestHandleBootstrap_RejectsShortPassword is the same shape as the
// username test, but for passwords. Important: the policy is
// documented and the handler should respect it.
func TestHandleBootstrap_RejectsShortPassword(t *testing.T) {
	t.Parallel()
	deps, _, _ := newTestAuthEnv(t)

	rr := httptest.NewRecorder()
	deps.handleBootstrap(rr, jsonRequest("POST", "/api/v1/auth/bootstrap", map[string]string{
		"username": "admin", "password": "short",
	}))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

// TestHandleLogin verifies the happy path: POST credentials, get a
// session cookie and a role response.
func TestHandleLogin_Success(t *testing.T) {
	t.Parallel()
	deps, store, _ := newTestAuthEnv(t)
	ctx := context.Background()

	// Seed: create a user via the service (bypasses the bootstrap
	// auto-login path so we test login in isolation).
	if _, err := deps.svc.CreateFirstUser(ctx, "alice", "hunter22"); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	rr := httptest.NewRecorder()
	deps.handleLogin(rr, jsonRequest("POST", "/api/v1/auth/login", map[string]string{
		"username": "alice", "password": "hunter22",
	}))
	if rr.Code != http.StatusOK {
		t.Fatalf("login: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Result().Cookies(); len(got) == 0 || got[0].Name != auth.SessionCookieName {
		t.Errorf("expected a session cookie, got %v", got)
	}
	// Session row should be present.
	var n int
	_ = store.DB().QueryRow("SELECT COUNT(*) FROM sessions").Scan(&n)
	if n != 1 {
		t.Errorf("expected 1 session after login, got %d", n)
	}
}

// TestHandleLogin_BadPassword verifies the failure path returns 401
// and does NOT create a session row.
func TestHandleLogin_BadPassword(t *testing.T) {
	t.Parallel()
	deps, store, _ := newTestAuthEnv(t)
	ctx := context.Background()
	if _, err := deps.svc.CreateFirstUser(ctx, "alice", "hunter22"); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	rr := httptest.NewRecorder()
	deps.handleLogin(rr, jsonRequest("POST", "/api/v1/auth/login", map[string]string{
		"username": "alice", "password": "wrong",
	}))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("login (bad pw): got %d, want 401; body=%s", rr.Code, rr.Body.String())
	}
	// No session row should have been written.
	var n int
	_ = store.DB().QueryRow("SELECT COUNT(*) FROM sessions").Scan(&n)
	if n != 0 {
		t.Errorf("expected 0 sessions after failed login, got %d", n)
	}
}

// TestHandleLogin_UnknownUser is the same as above but for a
// non-existent user. The error path must be identical (don't leak
// whether the username exists).
func TestHandleLogin_UnknownUser(t *testing.T) {
	t.Parallel()
	deps, _, _ := newTestAuthEnv(t)
	rr := httptest.NewRecorder()
	deps.handleLogin(rr, jsonRequest("POST", "/api/v1/auth/login", map[string]string{
		"username": "ghost", "password": "hunter22",
	}))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("login (unknown): got %d, want 401", rr.Code)
	}
}

// TestHandleLogin_RejectsEmptyFields verifies the handler validates
// body fields before reaching the service.
func TestHandleLogin_RejectsEmptyFields(t *testing.T) {
	t.Parallel()
	deps, _, _ := newTestAuthEnv(t)

	for _, body := range []map[string]string{
		{"username": "", "password": "x"},
		{"username": "alice", "password": ""},
		{},
	} {
		rr := httptest.NewRecorder()
		deps.handleLogin(rr, jsonRequest("POST", "/api/v1/auth/login", body))
		if rr.Code != http.StatusBadRequest {
			t.Errorf("empty-field login (body=%v): got %d, want 400", body, rr.Code)
		}
	}
}

// TestHandleLogout verifies the logout path: a valid session is
// destroyed, the response cookie is cleared, and the function is
// idempotent (a second logout is a no-op success).
func TestHandleLogout(t *testing.T) {
	t.Parallel()
	deps, store, _ := newTestAuthEnv(t)
	ctx := context.Background()

	// Seed user + log in to get a valid session token.
	if _, err := deps.svc.CreateFirstUser(ctx, "alice", "hunter22"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	token, _, err := deps.svc.Login(ctx, "alice", "hunter22", "ua", "1.2.3.4")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	// First logout: success, session gone from DB.
	r1 := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	r1.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
	rr1 := httptest.NewRecorder()
	deps.handleLogout(rr1, r1)
	if rr1.Code != http.StatusOK {
		t.Errorf("first logout: got %d, want 200", rr1.Code)
	}
	if cookies := rr1.Result().Cookies(); len(cookies) == 0 || cookies[0].MaxAge >= 0 {
		t.Errorf("expected clearing cookie (MaxAge<0), got %+v", cookies)
	}
	var n int
	_ = store.DB().QueryRow("SELECT COUNT(*) FROM sessions").Scan(&n)
	if n != 0 {
		t.Errorf("session not deleted; rows=%d", n)
	}

	// Second logout with same token: still 200 (idempotent), still no rows.
	r2 := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	r2.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
	rr2 := httptest.NewRecorder()
	deps.handleLogout(rr2, r2)
	if rr2.Code != http.StatusOK {
		t.Errorf("second logout: got %d, want 200", rr2.Code)
	}
}

// TestAuthMiddleware_RequiresCookieAndRejectsExpiredSession is the
// critical auth-gating test: a missing cookie = 401, an invalid
// cookie = 401, an expired session = 401 + cookie cleared.
func TestAuthMiddleware_RequiresCookieAndRejectsExpiredSession(t *testing.T) {
	t.Parallel()
	deps, store, _ := newTestAuthEnv(t)
	ctx := context.Background()
	if _, err := deps.svc.CreateFirstUser(ctx, "alice", "hunter22"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Reach into the DB and write an already-expired session.
	expired := time.Now().Add(-1 * time.Hour).UTC()
	_, err := store.DB().Exec(
		`INSERT INTO sessions (token, user_id, created_at, last_seen_at, expires_at, user_agent, ip) VALUES (?, 1, ?, ?, ?, 'ua', '1.2.3.4')`,
		"expired-token", time.Now().UTC(), time.Now().UTC(), expired)
	if err != nil {
		t.Fatalf("insert expired session: %v", err)
	}

	// Build a downstream handler that always returns 200 if reached.
	reached := false
	downstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})

	// 1. No cookie → 401, downstream not reached.
	r1 := httptest.NewRequest("GET", "/secret", nil)
	rr1 := httptest.NewRecorder()
	deps.svc.Middleware(downstream).ServeHTTP(rr1, r1)
	if rr1.Code != http.StatusUnauthorized {
		t.Errorf("no-cookie: got %d, want 401", rr1.Code)
	}
	if reached {
		t.Error("downstream reached without cookie")
	}

	// 2. Bad token → 401, cookie cleared.
	r2 := httptest.NewRequest("GET", "/secret", nil)
	r2.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "no-such-token"})
	rr2 := httptest.NewRecorder()
	deps.svc.Middleware(downstream).ServeHTTP(rr2, r2)
	if rr2.Code != http.StatusUnauthorized {
		t.Errorf("bad-cookie: got %d, want 401", rr2.Code)
	}
	if reached {
		t.Error("downstream reached with bad cookie")
	}
	// Verify the response cleared the cookie.
	if cookies := rr2.Result().Cookies(); len(cookies) == 0 || cookies[0].MaxAge >= 0 {
		t.Errorf("expected clearing cookie on bad session, got %+v", cookies)
	}

	// 3. Expired token → 401, downstream not reached, session deleted.
	r3 := httptest.NewRequest("GET", "/secret", nil)
	r3.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "expired-token"})
	rr3 := httptest.NewRecorder()
	deps.svc.Middleware(downstream).ServeHTTP(rr3, r3)
	if rr3.Code != http.StatusUnauthorized {
		t.Errorf("expired-cookie: got %d, want 401", rr3.Code)
	}
	if reached {
		t.Error("downstream reached with expired cookie")
	}
	var n int
	_ = store.DB().QueryRow("SELECT COUNT(*) FROM sessions WHERE token='expired-token'").Scan(&n)
	if n != 0 {
		t.Errorf("expired session should be deleted; rows=%d", n)
	}
}

// TestAuthMiddleware_AllowsValidSession verifies the happy path:
// the user is attached to the context, downstream is reached.
func TestAuthMiddleware_AllowsValidSession(t *testing.T) {
	t.Parallel()
	deps, _, _ := newTestAuthEnv(t)
	ctx := context.Background()
	if _, err := deps.svc.CreateFirstUser(ctx, "alice", "hunter22"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	token, _, err := deps.svc.Login(ctx, "alice", "hunter22", "ua", "1.2.3.4")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	var userFromCtx *storage.User
	downstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userFromCtx = auth.UserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	r := httptest.NewRequest("GET", "/secret", nil)
	r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
	rr := httptest.NewRecorder()
	deps.svc.Middleware(downstream).ServeHTTP(rr, r)
	if rr.Code != http.StatusOK {
		t.Fatalf("valid session: got %d, want 200", rr.Code)
	}
	if userFromCtx == nil {
		t.Fatal("downstream reached but no user on context")
	}
	if userFromCtx.Username != "alice" {
		t.Errorf("user = %q, want alice", userFromCtx.Username)
	}
}

// TestHandleStatus_ReportsBothStates is the endpoint that drives the
// UI's bootstrap detection. Bootstrap available when no users; not
// available when any user exists. Authenticated when a valid cookie
// is present.
func TestHandleStatus_ReportsBothStates(t *testing.T) {
	t.Parallel()
	deps, _, _ := newTestAuthEnv(t)
	ctx := context.Background()

	// 1. Empty install.
	rr1 := httptest.NewRecorder()
	deps.handleStatus(rr1, httptest.NewRequest("GET", "/api/v1/auth/status", nil))
	var r1 map[string]any
	_ = json.Unmarshal(rr1.Body.Bytes(), &r1)
	if r1["bootstrap_available"] != true {
		t.Errorf("empty: bootstrap_available = %v, want true", r1["bootstrap_available"])
	}
	if r1["authenticated"] != false {
		t.Errorf("empty: authenticated = %v, want false", r1["authenticated"])
	}

	// 2. After bootstrap.
	if _, err := deps.svc.CreateFirstUser(ctx, "alice", "hunter22"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rr2 := httptest.NewRecorder()
	deps.handleStatus(rr2, httptest.NewRequest("GET", "/api/v1/auth/status", nil))
	var r2 map[string]any
	_ = json.Unmarshal(rr2.Body.Bytes(), &r2)
	if r2["bootstrap_available"] != false {
		t.Errorf("post-bootstrap: bootstrap_available = %v, want false", r2["bootstrap_available"])
	}
	if r2["authenticated"] != false {
		t.Errorf("no-cookie: authenticated = %v, want false", r2["authenticated"])
	}

	// 3. With a valid session cookie.
	token, _, err := deps.svc.Login(ctx, "alice", "hunter22", "ua", "1.2.3.4")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	r3 := httptest.NewRequest("GET", "/api/v1/auth/status", nil)
	r3.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
	rr3 := httptest.NewRecorder()
	deps.handleStatus(rr3, r3)
	var r3Body map[string]any
	_ = json.Unmarshal(rr3.Body.Bytes(), &r3Body)
	if r3Body["authenticated"] != true {
		t.Errorf("with cookie: authenticated = %v, want true", r3Body["authenticated"])
	}
	if r3Body["username"] != "alice" {
		t.Errorf("with cookie: username = %v, want alice", r3Body["username"])
	}
}

// TestClientIP_TrustedProxyHonorsXFF exercises the security-critical
// audit-M18 path: XFF is honored only when the remote is in the
// trusted CIDR list. We reset the package-level trustedProxies slice
// at the start (and restore it at the end) so this test is hermetic
// against the others.
func TestClientIP_TrustedProxyHonorsXFF(t *testing.T) {
	// Don't t.Parallel — this test mutates package state.
	orig := trustedProxies
	defer func() { trustedProxies = orig }()
	trustedProxies = nil

	cases := []struct {
		name        string
		trustedCIDR string
		remoteAddr  string
		xff         string
		want        string
	}{
		{
			name:        "no_trusted_no_xff_uses_remote",
			trustedCIDR: "",
			remoteAddr:  "10.0.0.5:1234",
			xff:         "1.2.3.4",
			want:        "10.0.0.5",
		},
		{
			name:        "trusted_loopback_honors_xff",
			trustedCIDR: "127.0.0.1/32",
			remoteAddr:  "127.0.0.1:1234",
			xff:         "1.2.3.4",
			want:        "1.2.3.4",
		},
		{
			name:        "trusted_loopback_ignores_xff_for_non_loopback_remote",
			trustedCIDR: "127.0.0.1/32",
			remoteAddr:  "10.0.0.5:1234",
			xff:         "1.2.3.4",
			want:        "10.0.0.5",
		},
		{
			name:        "trusted_loopback_xff_with_multiple_hops_takes_first",
			trustedCIDR: "127.0.0.1/32",
			remoteAddr:  "127.0.0.1:1234",
			xff:         "1.2.3.4, 10.0.0.1",
			want:        "1.2.3.4",
		},
		{
			name:        "no_xff_at_all_uses_remote",
			trustedCIDR: "127.0.0.1/32",
			remoteAddr:  "127.0.0.1:1234",
			xff:         "",
			want:        "127.0.0.1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			trustedProxies = nil
			if tc.trustedCIDR != "" {
				(&v1AuthDeps{}).WithTrustedProxies(tc.trustedCIDR)
			}
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tc.remoteAddr
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := clientIP(r); got != tc.want {
				t.Errorf("clientIP = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestWithTrustedProxies_IgnoresInvalidCIDR is a regression guard:
// the constructor must not panic on garbage input.
func TestWithTrustedProxies_IgnoresInvalidCIDR(t *testing.T) {
	orig := trustedProxies
	defer func() { trustedProxies = orig }()
	trustedProxies = nil

	// Should not panic, should silently drop the bad entry.
	(&v1AuthDeps{}).WithTrustedProxies("not-a-cidr", "10.0.0.0/8", "")
	if len(trustedProxies) != 1 {
		t.Errorf("expected 1 valid CIDR kept, got %d", len(trustedProxies))
	}
}

// silenceUnused is a tiny helper to ensure server/chat/hub imports
// don't get flagged as unused in a future refactor. They are imported
// because newTestServerEnv (used by server tests) depends on them.
var _ = server.ErrInvalidTransition
var _ = chat.New
var _ = hub.NewHub
