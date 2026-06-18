package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Skomesh/MeshedServerTool/internal/bans"
	"github.com/Skomesh/MeshedServerTool/internal/chat"
	"github.com/Skomesh/MeshedServerTool/internal/hub"
	"github.com/Skomesh/MeshedServerTool/internal/server"
	"github.com/Skomesh/MeshedServerTool/internal/storage"
)

func newTestSettingsEnv(t *testing.T) (*v1SettingsDeps, *server.Manager, *storage.Store, string) {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "settings-test.db") +
		"?mode=memory&cache=shared&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	store, err := storage.OpenFromDSN(dsn)
	if err != nil {
		t.Fatalf("OpenFromDSN: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	h := hub.NewHub()
	t.Cleanup(func() { h.Close() })
	cs := chat.New(store)
	mgr, err := server.NewManager(store, h, cs)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	// Bans store, no rows yet.
	bansStore := bans.New(store)
	installDir := t.TempDir()
	return &v1SettingsDeps{store: store, manager: mgr, bans: bansStore}, mgr, store, installDir
}

// addSettingsServer registers a server in the DB+manager with a
// given install dir (so INI read/write lands somewhere real).
func addSettingsServer(t *testing.T, mgr *server.Manager, store *storage.Store, name, installDir string) {
	t.Helper()
	srv := &storage.Server{
		Name:       name,
		InstallDir: installDir,
		Port:       0,
		MaxPlayers: 8,
		Args: map[string]any{
			"executable": "/bin/sh",
			"argv":       []any{"-c", "sleep 0.1"},
		},
	}
	if err := store.Servers().CreateServer(context.Background(), srv); err != nil {
		t.Fatalf("CreateServer: %v", err)
	}
	mgr.AddServer(srv)
}

func (d *v1SettingsDeps) callSettings(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reqBody *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reqBody = bytes.NewReader(raw)
	} else {
		reqBody = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, reqBody)
	rr := httptest.NewRecorder()
	d.ServeHTTP(rr, r)
	return rr
}

// writeINIFile creates a real .ini file at installDir/relPath with
// the given content. Used to seed the read path.
func writeINIFile(t *testing.T, installDir, relPath, content string) {
	t.Helper()
	full := filepath.Join(installDir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestSettings_List_EmptyDir(t *testing.T) {
	t.Parallel()
	d, mgr, _, installDir := newTestSettingsEnv(t)
	addSettingsServer(t, mgr, d.store, "alpha", installDir)
	rr := d.callSettings(t, http.MethodGet, "/alpha", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var resp struct {
		Files []string `json:"files"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if resp.Files == nil || len(resp.Files) != 0 {
		t.Errorf("expected empty files array, got %v", resp.Files)
	}
}

func TestSettings_List_WithFiles(t *testing.T) {
	t.Parallel()
	d, mgr, _, installDir := newTestSettingsEnv(t)
	addSettingsServer(t, mgr, d.store, "alpha", installDir)
	writeINIFile(t, installDir, "BannedIDs.ini", "x=1\n")
	writeINIFile(t, installDir, "ServerSettings.ini", "y=2\n")
	rr := d.callSettings(t, http.MethodGet, "/alpha", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var resp struct {
		Files []string `json:"files"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if len(resp.Files) != 2 {
		t.Errorf("expected 2 files, got %v", resp.Files)
	}
	// Files should be basenames only.
	for _, f := range resp.Files {
		if strings.Contains(f, "/") {
			t.Errorf("expected basename, got %q", f)
		}
	}
}

func TestSettings_List_UnknownServer(t *testing.T) {
	t.Parallel()
	d, _, _, _ := newTestSettingsEnv(t)
	rr := d.callSettings(t, http.MethodGet, "/ghost", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rr.Code)
	}
}

func TestSettings_Read_HappyPath(t *testing.T) {
	t.Parallel()
	d, mgr, _, installDir := newTestSettingsEnv(t)
	addSettingsServer(t, mgr, d.store, "alpha", installDir)
	writeINIFile(t, installDir, "ServerSettings.ini", "[server]\nport=7777\n")
	rr := d.callSettings(t, http.MethodGet, "/alpha/ServerSettings.ini", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "port") || !strings.Contains(body, "7777") {
		t.Errorf("body should contain port=7777, got %s", body)
	}
}

func TestSettings_Read_PathTraversalRejected(t *testing.T) {
	t.Parallel()
	d, mgr, _, installDir := newTestSettingsEnv(t)
	addSettingsServer(t, mgr, d.store, "alpha", installDir)
	cases := []string{
		"../etc/passwd",
		"subdir/file.ini",
		"foo\\bar.ini",
		"notanini",
		"../BannedIDs.ini",
	}
	for _, p := range cases {
		rr := d.callSettings(t, http.MethodGet, "/alpha/"+p, nil)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("path %q: got %d, want 400", p, rr.Code)
		}
	}
}

func TestSettings_Write_HappyPath(t *testing.T) {
	t.Parallel()
	d, mgr, _, installDir := newTestSettingsEnv(t)
	addSettingsServer(t, mgr, d.store, "alpha", installDir)
	rr := d.callSettings(t, http.MethodPut, "/alpha/ServerSettings.ini", map[string]any{
		"sections": []map[string]any{
			{"name": "server", "entries": []map[string]string{
				{"key": "port", "value": "8080"},
			}},
		},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("write: %d body=%s", rr.Code, rr.Body.String())
	}
	// Read it back.
	rr = d.callSettings(t, http.MethodGet, "/alpha/ServerSettings.ini", nil)
	body := rr.Body.String()
	if !strings.Contains(body, "8080") {
		t.Errorf("read-back should contain 8080, got %s", body)
	}
	// File mode should be 0o600 (audit M17).
	info, err := os.Stat(filepath.Join(installDir, "ServerSettings.ini"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode: got %o, want 0o600", info.Mode().Perm())
	}
}

func TestSettings_Write_PathTraversalRejected(t *testing.T) {
	t.Parallel()
	d, mgr, _, installDir := newTestSettingsEnv(t)
	addSettingsServer(t, mgr, d.store, "alpha", installDir)
	rr := d.callSettings(t, http.MethodPut, "/alpha/../escape.ini", map[string]any{})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rr.Code)
	}
}

func TestSettings_Write_InvalidJSON(t *testing.T) {
	t.Parallel()
	d, mgr, _, installDir := newTestSettingsEnv(t)
	addSettingsServer(t, mgr, d.store, "alpha", installDir)
	r := httptest.NewRequest(http.MethodPut, "/alpha/ServerSettings.ini", strings.NewReader("{not json"))
	rr := httptest.NewRecorder()
	d.ServeHTTP(rr, r)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rr.Code)
	}
}

func TestSettings_SyncBans_HappyPath(t *testing.T) {
	t.Parallel()
	d, mgr, _, installDir := newTestSettingsEnv(t)
	addSettingsServer(t, mgr, d.store, "alpha", installDir)
	// Add a ban to the store.
	if _, err := d.bans.Add(context.Background(), "76561198000000001", "tester", "cheat", "admin"); err != nil {
		t.Fatalf("add ban: %v", err)
	}
	rr := d.callSettings(t, http.MethodPost, "/alpha/sync-bans", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("sync: %d", rr.Code)
	}
	var resp struct {
		OK       bool   `json:"ok"`
		BanCount int    `json:"ban_count"`
		Path     string `json:"path"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if !resp.OK {
		t.Errorf("ok=false")
	}
	if resp.BanCount != 1 {
		t.Errorf("ban_count: got %d, want 1", resp.BanCount)
	}
	// BannedIDs.ini should now exist in the install dir.
	full := filepath.Join(installDir, "BannedIDs.ini")
	if _, err := os.Stat(full); err != nil {
		t.Errorf("BannedIDs.ini not created: %v", err)
	}
}

func TestSettings_SyncBans_WrongMethod(t *testing.T) {
	t.Parallel()
	d, mgr, _, installDir := newTestSettingsEnv(t)
	addSettingsServer(t, mgr, d.store, "alpha", installDir)
	for _, m := range []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		rr := d.callSettings(t, m, "/alpha/sync-bans", nil)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s sync-bans: got %d, want 405", m, rr.Code)
		}
	}
}

func TestSettings_MethodNotAllowed(t *testing.T) {
	t.Parallel()
	d, mgr, _, installDir := newTestSettingsEnv(t)
	addSettingsServer(t, mgr, d.store, "alpha", installDir)
	// GET on item with body, PUT on collection.
	rr := d.callSettings(t, http.MethodPut, "/alpha", nil)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT collection: got %d, want 405", rr.Code)
	}
	// POST on item (not sync-bans).
	rr = d.callSettings(t, http.MethodPost, "/alpha/ServerSettings.ini", nil)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST item: got %d, want 405", rr.Code)
	}
}

// TestIsSafeINI_TableDriven covers the helper directly with
// table-driven inputs.
func TestIsSafeINI_TableDriven(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"plain", "ServerSettings.ini", true},
		{"upper ext", "BANNEDIDS.INI", true},
		{"traversal", "../etc/passwd.ini", false},
		{"slash", "sub/file.ini", false},
		{"backslash", "sub\\file.ini", false},
		{"empty", "", false},
		{"no ext", "noextension", false},
		{"txt", "config.txt", false},
		{"double dot", "..", false},
		{"nul", "foo\x00.ini", false},
		{"too long", strings.Repeat("a", 300) + ".ini", false},
	}
	for _, tc := range cases {
		if got := isSafeINI(tc.in); got != tc.want {
			t.Errorf("isSafeINI(%q): got %v, want %v", tc.in, got, tc.want)
		}
	}
}
