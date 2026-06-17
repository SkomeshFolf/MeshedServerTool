package chat

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Skomesh/MeshedServerTool/internal/storage"
)

// newTestChatStore opens an in-memory storage, runs migrations, and
// returns both the chat.Store and the underlying storage.Store so
// callers can assert on raw rows if they need to. The DB is closed
// during t.Cleanup.
func newTestChatStore(t *testing.T) (*Store, *storage.Store) {
	t.Helper()

	// Per-test in-memory DB: a unique DSN avoids cross-test bleed
	// because go test runs each test in the same process.
	dsn := "file:" + filepath.Join(t.TempDir(), "chat-test.db") +
		"?mode=memory&cache=shared&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"

	s, err := storage.OpenFromDSN(dsn)
	if err != nil {
		t.Fatalf("storage.OpenFromDSN: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	return New(s), s
}

func TestAdd_AndList(t *testing.T) {
	ctx := context.Background()
	cs, _ := newTestChatStore(t)

	if err := cs.Add(ctx, "srv1", "alice", "hello world"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := cs.Add(ctx, "srv1", "bob", "hi alice"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	// A message for a different server — must not appear in srv1's list.
	if err := cs.Add(ctx, "srv2", "carol", "other server"); err != nil {
		t.Fatalf("Add (srv2): %v", err)
	}

	got, err := cs.List(ctx, "srv1", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List(srv1) returned %d messages, want 2", len(got))
	}
	// Newest first.
	if got[0].PlayerName != "bob" || got[1].PlayerName != "alice" {
		t.Errorf("ordering wrong: got [%s, %s], want [bob, alice]",
			got[0].PlayerName, got[1].PlayerName)
	}
	for i, m := range got {
		if m.ServerName != "srv1" {
			t.Errorf("got[%d].ServerName = %q, want srv1", i, m.ServerName)
		}
		if m.Message == "" {
			t.Errorf("got[%d].Message is empty", i)
		}
		if m.At.IsZero() {
			t.Errorf("got[%d].At is zero", i)
		}
		if m.ID == 0 {
			t.Errorf("got[%d].ID is zero", i)
		}
	}
}

func TestList_RespectsLimit(t *testing.T) {
	ctx := context.Background()
	cs, _ := newTestChatStore(t)

	for i := 0; i < 10; i++ {
		if err := cs.Add(ctx, "srv", "p", "m"); err != nil {
			t.Fatalf("Add[%d]: %v", i, err)
		}
	}
	got, err := cs.List(ctx, "srv", 3)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("List(limit=3) returned %d, want 3", len(got))
	}
}

func TestList_SinceFiltersByAt(t *testing.T) {
	ctx := context.Background()
	cs, s := newTestChatStore(t)

	// Insert one message directly with a known timestamp in the past,
	// then a recent one with time.Now(). The List query orders by
	// `at DESC`; a "since" check (handled at the query level in this
	// test) between the two must return only the recent one. We use a
	// raw query here because the public List() doesn't expose a since
	// parameter — it always returns the most-recent N.
	past := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	if _, err := s.DB().ExecContext(ctx,
		`INSERT INTO chat_messages (server_name, player_name, message, at) VALUES (?, ?, ?, ?)`,
		"srv", "old", "old message", past,
	); err != nil {
		t.Fatalf("seed past: %v", err)
	}
	if err := cs.Add(ctx, "srv", "new", "new message"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	since := time.Now().UTC().Add(-1 * time.Minute)
	sinceStr := since.Format(time.RFC3339)
	rows, err := s.DB().QueryContext(ctx,
		`SELECT id, server_name, player_name, message, at
		 FROM chat_messages
		 WHERE server_name = ? AND at >= ?
		 ORDER BY at DESC`,
		"srv", sinceStr,
	)
	if err != nil {
		t.Fatalf("since query: %v", err)
	}
	var players []string
	for rows.Next() {
		var (
			id                                                                                  int64
			sn, pn, msg, atStr                                                                   string
		)
		if err := rows.Scan(&id, &sn, &pn, &msg, &atStr); err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
		players = append(players, pn)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	if len(players) != 1 {
		t.Fatalf("since query returned %d rows (%v), want 1", len(players), players)
	}
	if players[0] != "new" {
		t.Errorf("since query returned player %q, want 'new'", players[0])
	}
}

func TestPurgeOlderThan_KeepsRecent(t *testing.T) {
	ctx := context.Background()
	cs, s := newTestChatStore(t)

	// Seed: 1 old message, 1 recent.
	oldAt := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	if _, err := s.DB().ExecContext(ctx,
		`INSERT INTO chat_messages (server_name, player_name, message, at) VALUES (?, ?, ?, ?)`,
		"srv", "old", "old msg", oldAt,
	); err != nil {
		t.Fatalf("seed old: %v", err)
	}
	if err := cs.Add(ctx, "srv", "new", "recent msg"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Purge anything older than 1 minute.
	n, err := cs.PurgeOlderThan(ctx, 1*time.Minute)
	if err != nil {
		t.Fatalf("PurgeOlderThan: %v", err)
	}
	if n != 1 {
		t.Errorf("PurgeOlderThan reported %d rows affected, want 1", n)
	}

	// Only the recent message remains.
	got, err := cs.List(ctx, "srv", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("List after purge returned %d, want 1", len(got))
	}
	if got[0].PlayerName != "new" {
		t.Errorf("surviving message is %q, want 'new'", got[0].PlayerName)
	}

	// Second purge with the same threshold is a no-op.
	n2, err := cs.PurgeOlderThan(ctx, 1*time.Minute)
	if err != nil {
		t.Fatalf("PurgeOlderThan (2nd): %v", err)
	}
	if n2 != 0 {
		t.Errorf("second PurgeOlderThan affected %d rows, want 0", n2)
	}
}
