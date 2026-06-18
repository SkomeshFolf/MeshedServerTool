package api

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Skomesh/MeshedServerTool/internal/auth"
	"github.com/Skomesh/MeshedServerTool/internal/storage"
	_ "modernc.org/sqlite"
)

// newTestBackupEnv wires a v1BackupDeps plus a fresh in-memory DB
// and a data dir for the snapshot file. The deps also have a stub
// auth context available — see stubAdminContext / stubUserContext
// for the test helpers that put a user on the request context.
func newTestBackupEnv(t *testing.T) (*v1BackupDeps, *storage.Store) {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "backup-test.db") +
		"?mode=memory&cache=shared&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	store, err := storage.OpenFromDSN(dsn)
	if err != nil {
		t.Fatalf("storage.OpenFromDSN: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	dataDir := t.TempDir()
	deps := &v1BackupDeps{store: store, dataDir: dataDir}
	return deps, store
}

// stubAdminRequest builds an httptest request with a fake admin
// user on the context. We bypass the real auth middleware because
// we're testing the role check, not auth itself.
//
// To get a user on the context, we route through the real
// auth.Middleware with a valid session cookie. This is the only
// way to use the production userCtxKey (which is unexported).
func stubAdminRequest(method, target string) *http.Request {
	return stubUserRequest(method, target, storage.RoleAdmin)
}

// stubUserRequest does the same but with a non-admin role.
func stubUserRequest(method, target string, role storage.Role) *http.Request {
	// Each call gets its own DSN so the in-memory databases don't
	// share state across calls (mode=memory&cache=shared would
	// otherwise link them). t.TempDir would be ideal but we don't
	// have t here; use os.MkdirTemp and let the OS reap.
	dir, err := os.MkdirTemp("", "meshed-stubuser-*")
	if err != nil {
		panic(err)
	}
	dsn := "file:" + filepath.Join(dir, "stubuser-test.db") +
		"?mode=memory&cache=shared&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	store, err := storage.OpenFromDSN(dsn)
	if err != nil {
		panic(err)
	}
	svc := auth.NewService(store)

	ctx := context.Background()
	// Bootstrap admin.
	if _, err := svc.CreateFirstUser(ctx, "admin", "hunter22"); err != nil {
		panic(err)
	}
	// Add a non-admin via the auth service so the password is
	// properly bcrypt-hashed.
	if role == storage.RoleUser {
		hash, err := auth.HashPassword("x")
		if err != nil {
			panic(err)
		}
		if _, err := store.Users().CreateUser(ctx, "normaluser", hash, storage.RoleUser); err != nil {
			panic(err)
		}
	}

	// Log in as the appropriate user.
	var loginUser, loginPass string
	if role == storage.RoleAdmin {
		loginUser, loginPass = "admin", "hunter22"
	} else {
		loginUser, loginPass = "normaluser", "x"
	}
	token, _, err := svc.Login(ctx, loginUser, loginPass, "ua", "1.2.3.4")
	if err != nil {
		panic(err)
	}

	r := httptest.NewRequest(method, target, nil)
	r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})

	// Run the request through the auth middleware to put the user
	// on the context. We use a noop handler that records the
	// post-middleware request, which we then return.
	var got *http.Request
	h := svc.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if got == nil {
		panic("middleware did not invoke handler")
	}
	return got
}

// TestIsAdmin_BothRoles verifies the role check returns true for
// admin, false for user, false when no user.
func TestIsAdmin_BothRoles(t *testing.T) {
	t.Parallel()

	// 1. admin → isAdmin = true
	adminReq := stubAdminRequest("GET", "/api/v1/admin/backup")
	if !isAdmin(adminReq) {
		t.Error("admin should be admin")
	}

	// 2. non-admin → isAdmin = false
	normalReq := stubUserRequest("GET", "/api/v1/admin/backup", storage.RoleUser)
	if isAdmin(normalReq) {
		t.Error("non-admin should NOT be admin")
	}

	// 3. anonymous request: no user on context.
	anonReq := httptest.NewRequest("GET", "/api/v1/admin/backup", nil)
	if isAdmin(anonReq) {
		t.Error("anonymous should NOT be admin")
	}
}

// TestBackup_RejectsNonGet is the method gate.
func TestBackup_RejectsNonGet(t *testing.T) {
	t.Parallel()
	deps, _ := newTestBackupEnv(t)
	for _, method := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		rr := httptest.NewRecorder()
		r := stubAdminRequest(method, "/api/v1/admin/backup")
		deps.ServeHTTP(rr, r)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: got %d, want 405", method, rr.Code)
		}
	}
}

// TestBackup_RequiresAdminRole is the role gate.
func TestBackup_RequiresAdminRole(t *testing.T) {
	t.Parallel()
	deps, _ := newTestBackupEnv(t)
	rr := httptest.NewRecorder()
	r := stubUserRequest("GET", "/api/v1/admin/backup", storage.RoleUser)
	deps.ServeHTTP(rr, r)
	if rr.Code != http.StatusForbidden {
		t.Errorf("non-admin GET: got %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}

// TestBackup_ReturnsValidSQLite is the happy path: an admin GET
// returns a downloadable .db file that is itself a valid SQLite
// database with the same content as the source.
func TestBackup_ReturnsValidSQLite(t *testing.T) {
	t.Parallel()
	deps, store := newTestBackupEnv(t)
	ctx := context.Background()
	// Seed a user + a server so the backup has interesting content.
	if _, err := store.Users().CreateUser(ctx, "admin", "x", storage.RoleAdmin); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := store.Servers().CreateServer(ctx, &storage.Server{
		Name:       "backup-test-srv",
		InstallDir: "/tmp/x",
		Args:       map[string]any{"executable": "/bin/sh"},
	}); err != nil {
		t.Fatalf("seed server: %v", err)
	}

	rr := httptest.NewRecorder()
	r := stubAdminRequest("GET", "/api/v1/admin/backup")
	deps.ServeHTTP(rr, r)
	if rr.Code != http.StatusOK {
		t.Fatalf("admin GET: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	// Headers
	if ct := rr.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream", ct)
	}
	if cd := rr.Header().Get("Content-Disposition"); !contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q, want attachment", cd)
	}
	// Body is a valid SQLite database. Open it and check users.
	body := rr.Body.Bytes()
	if len(body) == 0 {
		t.Fatal("empty response body")
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	// Load the body as a SQLite file via the file: URI.
	tmp, err := os.CreateTemp(t.TempDir(), "verify-*.db")
	if err != nil {
		t.Fatalf("tmp: %v", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, bytes.NewReader(body)); err != nil {
		t.Fatalf("write: %v", err)
	}
	tmp.Close()
	vdb, err := sql.Open("sqlite", "file:"+tmp.Name()+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open verify: %v", err)
	}
	defer vdb.Close()
	var n int
	if err := vdb.QueryRow("SELECT COUNT(*) FROM users").Scan(&n); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if n != 1 {
		t.Errorf("users in backup = %d, want 1", n)
	}
	var sname string
	if err := vdb.QueryRow("SELECT name FROM servers").Scan(&sname); err != nil {
		t.Fatalf("verify servers: %v", err)
	}
	if sname != "backup-test-srv" {
		t.Errorf("server in backup = %q, want backup-test-srv", sname)
	}
}

// TestBackup_Chmod0600 verifies the snapshot file is written with
// 0o600 (owner read/write only) so the database contents aren't
// world-readable.
func TestBackup_Chmod0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod semantics differ on Windows")
	}
	t.Parallel()
	deps, _ := newTestBackupEnv(t)

	rr := httptest.NewRecorder()
	r := stubAdminRequest("GET", "/api/v1/admin/backup")
	deps.ServeHTTP(rr, r)
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rr.Code)
	}
	// The handler schedules an async cleanup 2 minutes after the
	// response. We have to look at the file before that runs.
	// Walk the data dir for the new meshed-backup-* file.
	entries, err := os.ReadDir(deps.dataDir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	var found string
	for _, e := range entries {
		if !e.IsDir() && len(e.Name()) > 14 && e.Name()[:14] == "meshed-backup-" {
			found = filepath.Join(deps.dataDir, e.Name())
			break
		}
	}
	if found == "" {
		t.Fatal("no backup file in data dir")
	}
	// Don't defer Remove — the handler's goroutine will, in 2 min.
	info, err := os.Stat(found)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	mode := info.Mode().Perm()
	if mode != 0o600 {
		t.Errorf("backup file mode = %o, want 0600", mode)
	}
}

// TestBackup_RejectsNoUser is the no-user-on-context case (the
// auth middleware would normally catch this, but defense in depth).
func TestBackup_RejectsNoUser(t *testing.T) {
	t.Parallel()
	deps, _ := newTestBackupEnv(t)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/v1/admin/backup", nil)
	deps.ServeHTTP(rr, r)
	if rr.Code != http.StatusForbidden {
		t.Errorf("no-user GET: got %d, want 403", rr.Code)
	}
}

// TestStoreBackup_Chmod0600 is the storage-level pin: Backup()
// itself writes a 0o600 file. Independent of the HTTP layer.
func TestStoreBackup_Chmod0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod semantics differ on Windows")
	}
	t.Parallel()
	dsn := "file:" + filepath.Join(t.TempDir(), "store-backup-test.db") +
		"?mode=memory&cache=shared&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	store, err := storage.OpenFromDSN(dsn)
	if err != nil {
		t.Fatalf("OpenFromDSN: %v", err)
	}
	defer store.Close()

	dest := filepath.Join(t.TempDir(), "snap.db")
	if err := store.Backup(dest); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("Backup() mode = %o, want 0600", got)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle ||
		indexOf(haystack, needle) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
