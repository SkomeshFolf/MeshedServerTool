package bans

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Skomesh/MeshedServerTool/internal/storage"
)

func newTestStore(t *testing.T) (*Store, *storage.Store) {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "bans-test.db") +
		"?mode=memory&cache=shared&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	s, err := storage.OpenFromDSN(dsn)
	if err != nil {
		t.Fatalf("OpenFromDSN: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return New(s), s
}

func TestBans_Add_NewBan(t *testing.T) {
	t.Parallel()
	bs, _ := newTestStore(t)
	ctx := context.Background()
	got, err := bs.Add(ctx, "76561198000000001", "tester", "cheat", "admin")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got.ID == 0 {
		t.Errorf("expected non-zero id, got 0")
	}
	if got.SteamID != "76561198000000001" {
		t.Errorf("steam_id: %q", got.SteamID)
	}
	if got.PlayerName != "tester" {
		t.Errorf("player_name: %q", got.PlayerName)
	}
	if got.BannedBy != "admin" {
		t.Errorf("banned_by: %q", got.BannedBy)
	}
	if got.BannedAt.IsZero() {
		t.Errorf("banned_at is zero")
	}
	if time.Since(got.BannedAt) > 5*time.Second {
		t.Errorf("banned_at too old: %v", got.BannedAt)
	}
}

func TestBans_Add_DuplicateUpdatesInPlace(t *testing.T) {
	t.Parallel()
	bs, _ := newTestStore(t)
	ctx := context.Background()
	first, err := bs.Add(ctx, "76561198000000001", "oldname", "old reason", "admin1")
	if err != nil {
		t.Fatalf("first add: %v", err)
	}
	second, err := bs.Add(ctx, "76561198000000001", "newname", "new reason", "admin2")
	if err != nil {
		t.Fatalf("second add: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("dedup should keep same id, got %d vs %d", first.ID, second.ID)
	}
	if second.PlayerName != "newname" {
		t.Errorf("player_name not updated: %q", second.PlayerName)
	}
	if second.Reason != "new reason" {
		t.Errorf("reason not updated: %q", second.Reason)
	}
	if second.BannedBy != "admin2" {
		t.Errorf("banned_by not updated: %q", second.BannedBy)
	}
}

func TestBans_GetByID(t *testing.T) {
	t.Parallel()
	bs, _ := newTestStore(t)
	ctx := context.Background()
	added, _ := bs.Add(ctx, "76561198000000001", "p", "r", "b")
	got, err := bs.GetByID(ctx, added.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.SteamID != added.SteamID {
		t.Errorf("steam_id mismatch: %q vs %q", got.SteamID, added.SteamID)
	}
}

func TestBans_GetByID_NotFound(t *testing.T) {
	t.Parallel()
	bs, _ := newTestStore(t)
	_, err := bs.GetByID(context.Background(), 99999)
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestBans_GetBySteamID(t *testing.T) {
	t.Parallel()
	bs, _ := newTestStore(t)
	ctx := context.Background()
	bs.Add(ctx, "76561198000000001", "p", "r", "b")
	got, err := bs.GetBySteamID(ctx, "76561198000000001")
	if err != nil {
		t.Fatalf("GetBySteamID: %v", err)
	}
	if got.SteamID != "76561198000000001" {
		t.Errorf("steam_id: %q", got.SteamID)
	}
}

func TestBans_GetBySteamID_NotFound(t *testing.T) {
	t.Parallel()
	bs, _ := newTestStore(t)
	_, err := bs.GetBySteamID(context.Background(), "76561198999999999")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestBans_List_Empty(t *testing.T) {
	t.Parallel()
	bs, _ := newTestStore(t)
	got, err := bs.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// The store returns nil for empty result; the API layer
	// converts to an empty slice for JSON. Document the
	// contract here.
	if len(got) != 0 {
		t.Errorf("expected 0 bans, got %d", len(got))
	}
}

func TestBans_List_OrderedNewestFirst(t *testing.T) {
	t.Parallel()
	bs, store := newTestStore(t)
	ctx := context.Background()
	// Insert three bans with explicit, distinct RFC3339 timestamps
	// to make ordering deterministic (the store's Add() uses
	// time.Now() which collides on the same second).
	for i, id := range []string{"111", "222", "333"} {
		ts := time.Now().UTC().Add(time.Duration(i+1) * time.Second).Format(time.RFC3339)
		if _, err := store.DB().ExecContext(ctx,
			`INSERT INTO bans (steam_id, player_name, reason, banned_by, banned_at) VALUES (?, '', '', '', ?)`,
			id, ts); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	got, err := bs.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 bans, got %d", len(got))
	}
	// Newest first → 333, 222, 111.
	if got[0].SteamID != "333" || got[1].SteamID != "222" || got[2].SteamID != "111" {
		t.Errorf("ordering wrong: %v %v %v", got[0].SteamID, got[1].SteamID, got[2].SteamID)
	}
}

func TestBans_Remove(t *testing.T) {
	t.Parallel()
	bs, _ := newTestStore(t)
	ctx := context.Background()
	added, _ := bs.Add(ctx, "76561198000000001", "p", "r", "b")
	if err := bs.Remove(ctx, added.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	// Verify gone.
	_, err := bs.GetByID(ctx, added.ID)
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("expected ErrNotFound after remove, got %v", err)
	}
}

func TestBans_Remove_NotFound(t *testing.T) {
	t.Parallel()
	bs, _ := newTestStore(t)
	err := bs.Remove(context.Background(), 99999)
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestBans_ListSteamIDs(t *testing.T) {
	t.Parallel()
	bs, store := newTestStore(t)
	ctx := context.Background()
	for i, id := range []string{"111", "222", "333"} {
		ts := time.Now().UTC().Add(time.Duration(i+1) * time.Second).Format(time.RFC3339)
		if _, err := store.DB().ExecContext(ctx,
			`INSERT INTO bans (steam_id, player_name, reason, banned_by, banned_at) VALUES (?, '', '', '', ?)`,
			id, ts); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	ids, err := bs.ListSteamIDs(ctx)
	if err != nil {
		t.Fatalf("ListSteamIDs: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("expected 3 ids, got %d", len(ids))
	}
	// Newest first.
	if ids[0] != "333" || ids[1] != "222" || ids[2] != "111" {
		t.Errorf("ordering wrong: %v", ids)
	}
}

func TestBans_ContextCancel(t *testing.T) {
	t.Parallel()
	bs, _ := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before call
	_, err := bs.Add(ctx, "76561198000000001", "p", "r", "b")
	if err == nil {
		t.Errorf("expected error from cancelled context, got nil")
	}
}
