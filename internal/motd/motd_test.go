package motd

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Skomesh/MeshedServerTool/internal/storage"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "motd-test.db") +
		"?mode=memory&cache=shared&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	s, err := storage.OpenFromDSN(dsn)
	if err != nil {
		t.Fatalf("OpenFromDSN: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return New(s)
}

func TestMotd_GetForServer_EmptyAllPaths(t *testing.T) {
	t.Parallel()
	ms := newTestStore(t)
	m, perServer, err := ms.GetForServer(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("GetForServer: %v", err)
	}
	if perServer {
		t.Errorf("expected perServer=false, got true")
	}
	if m == nil {
		t.Fatal("expected non-nil MOTD placeholder")
	}
	if m.Message != "" {
		t.Errorf("expected empty message, got %q", m.Message)
	}
	if m.Enabled {
		t.Errorf("expected disabled, got enabled")
	}
}

func TestMotd_GetGlobal_Empty(t *testing.T) {
	t.Parallel()
	ms := newTestStore(t)
	g, err := ms.GetGlobal(context.Background())
	if err != nil {
		t.Fatalf("GetGlobal: %v", err)
	}
	if g != nil {
		t.Errorf("expected nil, got %+v", g)
	}
}

func TestMotd_SetGetGlobal(t *testing.T) {
	t.Parallel()
	ms := newTestStore(t)
	ctx := context.Background()
	got, err := ms.SetGlobal(ctx, "hello world", "admin")
	if err != nil {
		t.Fatalf("SetGlobal: %v", err)
	}
	if got.Message != "hello world" {
		t.Errorf("message: %q", got.Message)
	}
	if !got.Enabled {
		t.Errorf("expected enabled after SetGlobal, got false")
	}
	if got.UpdatedBy != "admin" {
		t.Errorf("updated_by: %q", got.UpdatedBy)
	}
	if got.ServerName != "*" {
		t.Errorf("server_name sentinel: %q", got.ServerName)
	}
	if got.UpdatedAt.IsZero() {
		t.Errorf("updated_at is zero")
	}
	// Read it back.
	g, err := ms.GetGlobal(ctx)
	if err != nil {
		t.Fatalf("GetGlobal: %v", err)
	}
	if g == nil || g.Message != "hello world" {
		t.Errorf("GetGlobal round-trip mismatch: %+v", g)
	}
}

func TestMotd_SetGlobal_OverwriteUpdates(t *testing.T) {
	t.Parallel()
	ms := newTestStore(t)
	ctx := context.Background()
	ms.SetGlobal(ctx, "first", "admin1")
	// Wait a tick so the second update's timestamp differs.
	time.Sleep(1100 * time.Millisecond)
	ms.SetGlobal(ctx, "second", "admin2")
	g, _ := ms.GetGlobal(ctx)
	if g.Message != "second" {
		t.Errorf("overwrite: got %q, want second", g.Message)
	}
}

func TestMotd_GetForServer_FallsBackToGlobal(t *testing.T) {
	t.Parallel()
	ms := newTestStore(t)
	ctx := context.Background()
	ms.SetGlobal(ctx, "global default", "admin")
	m, perServer, err := ms.GetForServer(ctx, "alpha")
	if err != nil {
		t.Fatalf("GetForServer: %v", err)
	}
	if perServer {
		t.Errorf("perServer should be false on fallback, got true")
	}
	if m == nil || m.Message != "global default" {
		t.Errorf("expected global default, got %+v", m)
	}
	// Server name should be surfaced for context (per the code
	// comment in GetForServer).
	if m.ServerName != "alpha" {
		t.Errorf("server_name: got %q, want alpha", m.ServerName)
	}
}

func TestMotd_GetForServer_PerServerWins(t *testing.T) {
	t.Parallel()
	ms := newTestStore(t)
	ctx := context.Background()
	ms.SetGlobal(ctx, "global", "admin")
	ms.SetForServer(ctx, "alpha", "per-server", true, "admin")
	m, perServer, err := ms.GetForServer(ctx, "alpha")
	if err != nil {
		t.Fatalf("GetForServer: %v", err)
	}
	if !perServer {
		t.Errorf("perServer should be true")
	}
	if m.Message != "per-server" {
		t.Errorf("expected per-server to win, got %q", m.Message)
	}
}

func TestMotd_SetForServer(t *testing.T) {
	t.Parallel()
	ms := newTestStore(t)
	ctx := context.Background()
	got, err := ms.SetForServer(ctx, "alpha", "msg", true, "admin")
	if err != nil {
		t.Fatalf("SetForServer: %v", err)
	}
	if got.ServerName != "alpha" {
		t.Errorf("server_name: %q", got.ServerName)
	}
	if !got.Enabled {
		t.Errorf("expected enabled")
	}
}

func TestMotd_SetForServer_UpdateExisting(t *testing.T) {
	t.Parallel()
	ms := newTestStore(t)
	ctx := context.Background()
	first, _ := ms.SetForServer(ctx, "alpha", "v1", true, "admin1")
	time.Sleep(1100 * time.Millisecond)
	second, _ := ms.SetForServer(ctx, "alpha", "v2", false, "admin2")
	// Same row, different fields — the timestamp is what proves
	// the row was actually updated.
	if !second.UpdatedAt.After(first.UpdatedAt) {
		t.Errorf("UpdatedAt should advance on update: first=%v second=%v", first.UpdatedAt, second.UpdatedAt)
	}
	if second.Message != "v2" {
		t.Errorf("message not updated: %q", second.Message)
	}
	if second.Enabled {
		t.Errorf("enabled not flipped to false")
	}
	if second.UpdatedBy != "admin2" {
		t.Errorf("updated_by: %q", second.UpdatedBy)
	}
}

func TestMotd_DeleteForServer(t *testing.T) {
	t.Parallel()
	ms := newTestStore(t)
	ctx := context.Background()
	ms.SetForServer(ctx, "alpha", "msg", true, "admin")
	if err := ms.DeleteForServer(ctx, "alpha"); err != nil {
		t.Fatalf("DeleteForServer: %v", err)
	}
	// After delete, GetForServer should fall back to global
	// (or empty if no global).
	_, perServer, _ := ms.GetForServer(ctx, "alpha")
	if perServer {
		t.Errorf("perServer should be false after delete")
	}
}

func TestMotd_DeleteForServer_NotFound(t *testing.T) {
	t.Parallel()
	ms := newTestStore(t)
	err := ms.DeleteForServer(context.Background(), "ghost")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestMotd_List_OrderedByServerName(t *testing.T) {
	t.Parallel()
	ms := newTestStore(t)
	ctx := context.Background()
	for _, name := range []string{"zulu", "alpha", "mike"} {
		ms.SetForServer(ctx, name, "msg-"+name, true, "admin")
	}
	got, err := ms.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3, got %d", len(got))
	}
	// Alphabetical: alpha, mike, zulu.
	if got[0].ServerName != "alpha" || got[1].ServerName != "mike" || got[2].ServerName != "zulu" {
		t.Errorf("ordering wrong: %s %s %s", got[0].ServerName, got[1].ServerName, got[2].ServerName)
	}
}

func TestMotd_List_Empty(t *testing.T) {
	t.Parallel()
	ms := newTestStore(t)
	got, err := ms.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// Store returns nil for empty; the API handler converts
	// to an empty slice for JSON. Document the contract here.
	if len(got) != 0 {
		t.Errorf("expected 0 entries, got %d", len(got))
	}
}
