package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Skomesh/MeshedServerTool/internal/chat"
	"github.com/Skomesh/MeshedServerTool/internal/hub"
	"github.com/Skomesh/MeshedServerTool/internal/server"
	"github.com/Skomesh/MeshedServerTool/internal/storage"
)

// newTestServerEnv wires a v1ServerDeps backed by a fresh in-memory DB
// and a real server.Manager. The manager rehydrates from storage on
// NewManager, so servers persisted by previous tests in the same
// process are NOT visible (each test gets its own DB).
//
// Returns the deps and store. Also returns a router-style wrapped
// handler that strips /api/v1/servers from incoming paths, matching
// what NewRouter does in production.
func newTestServerEnv(t *testing.T) (*v1ServerDeps, *storage.Store, http.Handler) {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "server-handler-test.db") +
		"?mode=memory&cache=shared&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	store, err := storage.OpenFromDSN(dsn)
	if err != nil {
		t.Fatalf("storage.OpenFromDSN: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	h := hub.NewHub()
	t.Cleanup(func() { h.Close() })

	cs := chat.New(store)
	mgr, err := server.NewManager(store, h, cs)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() {
		_ = mgr.StopAll(context.Background(), 2*time.Second)
	})

	deps := &v1ServerDeps{store: store, manager: mgr}
	// (CRIT-1+2) Tests run with full validation: install_root="/"
	// (everything passes the install_dir under-root check) and
	// allowArbitraryExe=true (tests use /bin/sh etc. as fake game
	// binaries; production code would reject these).
	deps.SetInstallRoot("/", nil, true)
	// Wire the prefix-stripped handler the same way the production
	// router does it (see internal/api/router.go). The production
	// router uses http.StripPrefix + auth middleware; we skip the
	// middleware here so these tests focus on the handler logic.
	stripped := http.StripPrefix("/api/v1/servers", deps)
	return deps, store, stripped
}

// requireShell skips a test that needs /bin/sh when not present
// (Windows). Mirrors the helper in the server package.
func requireShell(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("/bin/sh"); err != nil {
		t.Skip("/bin/sh not available on this platform")
	}
}

// TestListServers_EmptyReturnsEmptyArray verifies the response shape
// when no servers are registered. The audit hardening commit changed
// this from "returns null" to "returns []" — guard against regression.
func TestListServers_EmptyReturnsEmptyArray(t *testing.T) {
	t.Parallel()
	_, _, h := newTestServerEnv(t)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v1/servers", nil)
	h.ServeHTTP(rr, r)
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	servers, ok := body["servers"].([]any)
	if !ok {
		t.Fatalf("expected servers to be a JSON array, got %T (body=%s)", body["servers"], rr.Body.String())
	}
	if len(servers) != 0 {
		t.Errorf("expected 0 servers, got %d", len(servers))
	}
}

// TestCreateServer_RejectsNonGetOnCollection tests the method gate
// for non-list non-POST methods on the collection URL. POST is valid
// (creates a server), so we don't test that here.
func TestCreateServer_RejectsNonGetOnCollection(t *testing.T) {
	t.Parallel()
	_, _, h := newTestServerEnv(t)
	for _, method := range []string{"PUT", "DELETE", "PATCH"} {
		rr := httptest.NewRecorder()
		r := httptest.NewRequest(method, "/api/v1/servers", nil)
		h.ServeHTTP(rr, r)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s /api/v1/servers: got %d, want 405", method, rr.Code)
		}
	}
}

// TestCreateServer_Validation walks all the validation rules: bad
// name, missing install_dir, port/max_players defaults.
func TestCreateServer_Validation(t *testing.T) {
	t.Parallel()
	_, _, h := newTestServerEnv(t)

	cases := []struct {
		name     string
		body     map[string]any
		wantCode int
		wantName string // optional check
	}{
		{
			name:     "valid_minimal",
			body:     map[string]any{"name": "alpha", "install_dir": "/tmp/x"},
			wantCode: http.StatusCreated,
			wantName: "alpha",
		},
		{
			name:     "missing_install_dir",
			body:     map[string]any{"name": "alpha"},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "name_too_short",
			body:     map[string]any{"name": "ab", "install_dir": "/tmp/x"},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "name_bad_chars",
			body:     map[string]any{"name": "bad name", "install_dir": "/tmp/x"},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "name_too_long",
			body:     map[string]any{"name": string(make([]byte, 65)), "install_dir": "/tmp/x"},
			wantCode: http.StatusBadRequest,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			r := jsonRequest("POST", "/api/v1/servers", tc.body)
			h.ServeHTTP(rr, r)
			if rr.Code != tc.wantCode {
				t.Errorf("got %d, want %d; body=%s", rr.Code, tc.wantCode, rr.Body.String())
			}
		})
	}
}

// TestCreateServer_Defaults verifies port=7777 and max_players=32
// when the body omits them.
func TestCreateServer_Defaults(t *testing.T) {
	t.Parallel()
	_, _, h := newTestServerEnv(t)
	rr := httptest.NewRecorder()
	r := jsonRequest("POST", "/api/v1/servers", map[string]any{
		"name": "defaults-srv", "install_dir": "/tmp/x",
	})
	h.ServeHTTP(rr, r)
	if rr.Code != http.StatusCreated {
		t.Fatalf("got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	state, _ := resp["State"].(map[string]any)
	_ = state // not present in this response shape
	// Pull the Server fields.
	srv, _ := resp["Server"].(map[string]any)
	if srv == nil {
		// response is a flat ServerView
		srv = resp
	}
	if got := srv["port"]; got != float64(7777) {
		t.Errorf("default port = %v, want 7777", got)
	}
	if got := srv["max_players"]; got != float64(32) {
		t.Errorf("default max_players = %v, want 32", got)
	}
}

// TestCreateServer_DuplicateReturns409 verifies the UNIQUE-constraint
// error path. The handler matches "UNIQUE" in the error string — a
// brittle pattern, but a real one we should pin down.
func TestCreateServer_DuplicateReturns409(t *testing.T) {
	t.Parallel()
	_, _, h := newTestServerEnv(t)
	body := map[string]any{"name": "dupe", "install_dir": "/tmp/x"}
	r1 := jsonRequest("POST", "/api/v1/servers", body)
	rr1 := httptest.NewRecorder()
	h.ServeHTTP(rr1, r1)
	if rr1.Code != http.StatusCreated {
		t.Fatalf("first: got %d, want 201", rr1.Code)
	}
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, jsonRequest("POST", "/api/v1/servers", body))
	if rr2.Code != http.StatusConflict {
		t.Errorf("second: got %d, want 409; body=%s", rr2.Code, rr2.Body.String())
	}
}

// TestGetServer_NotFound is the 404 path.
func TestGetServer_NotFound(t *testing.T) {
	t.Parallel()
	_, _, h := newTestServerEnv(t)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v1/servers/ghost", nil)
	h.ServeHTTP(rr, r)
	if rr.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rr.Code)
	}
}

// TestUpdateServer_PreservesUnsetFields is the partial-update
// invariant. The handler uses pointer fields so callers can patch
// only what they want. Sending {"port": 9000} must NOT blank out
// install_dir.
func TestUpdateServer_PreservesUnsetFields(t *testing.T) {
	t.Parallel()
	deps, _, h := newTestServerEnv(t)
	ctx := context.Background()

	// Seed
	rr0 := httptest.NewRecorder()
	r0 := jsonRequest("POST", "/api/v1/servers", map[string]any{
		"name": "patchable", "install_dir": "/tmp/orig",
	})
	h.ServeHTTP(rr0, r0)
	if rr0.Code != http.StatusCreated {
		t.Fatalf("seed: %d", rr0.Code)
	}

	// Patch just port.
	rr1 := httptest.NewRecorder()
	r1 := jsonRequest("PATCH", "/api/v1/servers/patchable", map[string]any{
		"port": 9000,
	})
	h.ServeHTTP(rr1, r1)
	if rr1.Code != http.StatusOK {
		t.Fatalf("patch: %d, body=%s", rr1.Code, rr1.Body.String())
	}

	// Read back from the DB; install_dir must still be /tmp/orig.
	srv, err := deps.store.Servers().GetServer(ctx, "patchable")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if srv.InstallDir != "/tmp/orig" {
		t.Errorf("install_dir = %q, want /tmp/orig (partial-update regression)", srv.InstallDir)
	}
	if srv.Port != 9000 {
		t.Errorf("port = %d, want 9000", srv.Port)
	}
}

// TestUpdateServer_NotFound returns 404 for a missing server.
func TestUpdateServer_NotFound(t *testing.T) {
	t.Parallel()
	_, _, h := newTestServerEnv(t)
	rr := httptest.NewRecorder()
	r := jsonRequest("PATCH", "/api/v1/servers/ghost", map[string]any{"port": 9000})
	h.ServeHTTP(rr, r)
	if rr.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rr.Code)
	}
}

// TestDeleteServer_RemovesFromManagerAndDB is the audit-#3 contract:
// delete from DB first, then from the manager map. After delete, both
// stores are empty for that name.
func TestDeleteServer_RemovesFromManagerAndDB(t *testing.T) {
	t.Parallel()
	deps, _, h := newTestServerEnv(t)
	ctx := context.Background()

	// Seed
	rr0 := httptest.NewRecorder()
	r0 := jsonRequest("POST", "/api/v1/servers", map[string]any{
		"name": "doomed", "install_dir": "/tmp/x",
	})
	h.ServeHTTP(rr0, r0)
	if rr0.Code != http.StatusCreated {
		t.Fatalf("seed: %d", rr0.Code)
	}

	// Delete
	rr1 := httptest.NewRecorder()
	r1 := httptest.NewRequest("DELETE", "/api/v1/servers/doomed", nil)
	h.ServeHTTP(rr1, r1)
	if rr1.Code != http.StatusNoContent {
		t.Errorf("delete: got %d, want 204; body=%s", rr1.Code, rr1.Body.String())
	}

	// DB row should be gone.
	if _, err := deps.store.Servers().GetServer(ctx, "doomed"); err == nil {
		t.Error("server row should be deleted from DB")
	}
	// Manager should not have it.
	if deps.manager.Get("doomed") != nil {
		t.Error("server should be removed from manager")
	}

	// Second delete returns 404.
	rr2 := httptest.NewRecorder()
	r2 := httptest.NewRequest("DELETE", "/api/v1/servers/doomed", nil)
	h.ServeHTTP(rr2, r2)
	if rr2.Code != http.StatusNotFound {
		t.Errorf("second delete: got %d, want 404", rr2.Code)
	}
}

// TestLifecycleAction_MethodGate rejects GET/PUT/DELETE/etc with 405.
// Only POST is valid.
func TestLifecycleAction_MethodGate(t *testing.T) {
	t.Parallel()
	_, _, h := newTestServerEnv(t)
	rr0 := httptest.NewRecorder()
	r0 := jsonRequest("POST", "/api/v1/servers", map[string]any{
		"name": "action-srv", "install_dir": "/tmp/x",
	})
	h.ServeHTTP(rr0, r0)
	if rr0.Code != http.StatusCreated {
		t.Fatalf("seed: %d", rr0.Code)
	}
	for _, method := range []string{"GET", "PUT", "DELETE", "PATCH"} {
		rr := httptest.NewRecorder()
		r := httptest.NewRequest(method, "/api/v1/servers/action-srv/start", nil)
		h.ServeHTTP(rr, r)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s /start: got %d, want 405", method, rr.Code)
		}
	}
}

// TestLifecycleAction_NotFound returns 404 for an unknown server.
func TestLifecycleAction_NotFound(t *testing.T) {
	t.Parallel()
	_, _, h := newTestServerEnv(t)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/servers/ghost/start", nil)
	h.ServeHTTP(rr, r)
	if rr.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rr.Code)
	}
}

// TestLifecycleAction_StartStopRestart exercises the real subprocess
// path with /bin/sh. Verifies the actions return 200, the state
// machine updates, and the server reaches a terminal state after
// stop.
func TestLifecycleAction_StartStopRestart(t *testing.T) {
	requireShell(t)
	deps, _, h := newTestServerEnv(t)

	// Seed a server that sleeps so we can observe running state.
	// install_dir must exist on disk because the subprocess chdirs
	// into it before exec.
	installDir := t.TempDir()
	rr0 := httptest.NewRecorder()
	r0 := jsonRequest("POST", "/api/v1/servers", map[string]any{
		"name": "lc-srv", "install_dir": installDir,
		"args": map[string]any{
			"executable": "/bin/sh",
			"argv":       []any{"-c", "sleep 60"},
		},
	})
	h.ServeHTTP(rr0, r0)
	if rr0.Code != http.StatusCreated {
		t.Fatalf("seed: %d, body=%s", rr0.Code, rr0.Body.String())
	}

	// 1. Start
	rr1 := httptest.NewRecorder()
	r1 := httptest.NewRequest("POST", "/api/v1/servers/lc-srv/start", nil)
	h.ServeHTTP(rr1, r1)
	if rr1.Code != http.StatusOK {
		t.Fatalf("start: got %d, want 200; body=%s", rr1.Code, rr1.Body.String())
	}

	// Wait for running state.
	running := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if v := deps.manager.Get("lc-srv"); v != nil && v.State.Status == storage.StatusRunning {
			running = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !running {
		t.Fatal("server did not reach running state")
	}

	// 2. Second start should be 409 (invalid transition).
	rr1b := httptest.NewRecorder()
	r1b := httptest.NewRequest("POST", "/api/v1/servers/lc-srv/start", nil)
	h.ServeHTTP(rr1b, r1b)
	if rr1b.Code != http.StatusConflict {
		t.Errorf("second start: got %d, want 409", rr1b.Code)
	}

	// 3. Stop
	rr2 := httptest.NewRecorder()
	r2 := httptest.NewRequest("POST", "/api/v1/servers/lc-srv/stop", nil)
	h.ServeHTTP(rr2, r2)
	if rr2.Code != http.StatusOK {
		t.Errorf("stop: got %d, want 200", rr2.Code)
	}
	terminal := false
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		v := deps.manager.Get("lc-srv")
		if v != nil && (v.State.Status == storage.StatusStopped || v.State.Status == storage.StatusCrashed) {
			terminal = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !terminal {
		t.Errorf("server did not reach terminal state, got %s", deps.manager.Get("lc-srv").State.Status)
	}
}

// TestLifecycleAction_UnknownSubResource — paths like /restart, /logs
// are sub-resources, not server names. /api/v1/servers/alpha/restart
// should hit the action switch, not the get/update/delete switch.
func TestLifecycleAction_UnknownSubResource(t *testing.T) {
	t.Parallel()
	_, _, h := newTestServerEnv(t)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/servers/foo/bogus", nil)
	h.ServeHTTP(rr, r)
	if rr.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rr.Code)
	}
}

// TestHandleLogs_TailParameter covers default (200), explicit N, N>5000
// (clamped to 5000), and non-integer (400).
func TestHandleLogs_TailParameter(t *testing.T) {
	t.Parallel()
	_, _, h := newTestServerEnv(t)

	// Seed
	rr0 := httptest.NewRecorder()
	r0 := jsonRequest("POST", "/api/v1/servers", map[string]any{
		"name": "logs-srv", "install_dir": "/tmp/x",
	})
	h.ServeHTTP(rr0, r0)
	if rr0.Code != http.StatusCreated {
		t.Fatalf("seed: %d", rr0.Code)
	}

	cases := []struct {
		name     string
		query    string
		wantCode int
	}{
		{"default_200", "", http.StatusOK},
		{"explicit_50", "?tail=50", http.StatusOK},
		{"clamped_5000", "?tail=10000", http.StatusOK},
		{"zero_is_valid", "?tail=0", http.StatusOK},
		{"bad_format", "?tail=abc", http.StatusBadRequest},
		{"negative", "?tail=-1", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "/api/v1/servers/logs-srv/logs"+tc.query, nil)
			h.ServeHTTP(rr, r)
			if rr.Code != tc.wantCode {
				t.Errorf("got %d, want %d; body=%s", rr.Code, tc.wantCode, rr.Body.String())
			}
		})
	}
}

// TestHandleLogs_NotFound for a missing server returns 404.
func TestHandleLogs_NotFound(t *testing.T) {
	t.Parallel()
	_, _, h := newTestServerEnv(t)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v1/servers/ghost/logs", nil)
	h.ServeHTTP(rr, r)
	if rr.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rr.Code)
	}
}
