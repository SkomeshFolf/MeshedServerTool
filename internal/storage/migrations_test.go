package storage

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// newTestStore opens an in-memory SQLite, runs migrate(), and registers
// cleanup. The store's underlying *sql.DB has SetMaxOpenConns(1) so the
// migrations (which use a transaction) and any test queries serialise
// safely without us juggling locks.
func newTestStore(t *testing.T) *Store {
	t.Helper()

	// Per-test in-memory DB: a unique DSN avoids cross-test bleed
	// because go test runs each test in the same process. The
	// "file:<tmpdir>/...&mode=memory" form is a per-DSN memory
	// database in modernc.org/sqlite.
	dsn := "file:" + filepath.Join(t.TempDir(), "test.db") +
		"?mode=memory&cache=shared&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"

	s, err := OpenFromDSN(dsn)
	if err != nil {
		t.Fatalf("OpenFromDSN: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestMigrate_AppliesAllVersions(t *testing.T) {
	s := newTestStore(t)

	// The schema_version table only stores (version, applied_at); the
	// human-readable migration `name` is a Go-side concept, not
	// persisted. So we just verify every defined migration version is
	// recorded in order.
	rows, err := s.db.Query(`SELECT version FROM schema_version ORDER BY version`)
	if err != nil {
		t.Fatalf("select schema_version: %v", err)
	}
	defer rows.Close()

	var gotVersions []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan: %v", err)
		}
		gotVersions = append(gotVersions, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}

	want := allMigrations()
	if len(gotVersions) != len(want) {
		t.Fatalf("applied %d migrations, want %d", len(gotVersions), len(want))
	}
	for i, m := range want {
		if gotVersions[i] != m.version {
			t.Errorf("migration %d: got version %d, want %d", i, gotVersions[i], m.version)
		}
	}

	// currentVersion should report the highest known version.
	if v, err := s.currentVersion(); err != nil {
		t.Fatalf("currentVersion: %v", err)
	} else if v != want[len(want)-1].version {
		t.Errorf("currentVersion = %d, want %d", v, want[len(want)-1].version)
	}

	// Every applied_at must be a parseable RFC3339 timestamp.
	tsRows, err := s.db.Query(`SELECT version, applied_at FROM schema_version`)
	if err != nil {
		t.Fatalf("select applied_at: %v", err)
	}
	defer tsRows.Close()
	for tsRows.Next() {
		var v int
		var at string
		if err := tsRows.Scan(&v, &at); err != nil {
			t.Fatalf("scan ts: %v", err)
		}
		if _, err := time.Parse(time.RFC3339, at); err != nil {
			t.Errorf("migration %d has invalid applied_at %q: %v", v, at, err)
		}
	}
}

func TestMigrate_IsIdempotent(t *testing.T) {
	s := newTestStore(t)

	// Snapshot the rows after the first migrate.
	first, err := s.db.Query(`SELECT version, applied_at FROM schema_version ORDER BY version`)
	if err != nil {
		t.Fatalf("select (first): %v", err)
	}
	type row struct {
		version   int
		appliedAt string
	}
	var snapshot []row
	for first.Next() {
		var r row
		if err := first.Scan(&r.version, &r.appliedAt); err != nil {
			t.Fatalf("scan: %v", err)
		}
		snapshot = append(snapshot, r)
	}
	first.Close()
	if err := first.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}

	// Re-run migrate(). It must succeed and the row set must be unchanged.
	if err := s.migrate(); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}

	second, err := s.db.Query(`SELECT version, applied_at FROM schema_version ORDER BY version`)
	if err != nil {
		t.Fatalf("select (second): %v", err)
	}
	var after []row
	for second.Next() {
		var r row
		if err := second.Scan(&r.version, &r.appliedAt); err != nil {
			t.Fatalf("scan: %v", err)
		}
		after = append(after, r)
	}
	second.Close()
	if err := second.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}

	if len(snapshot) != len(after) {
		t.Fatalf("row count changed across re-migrate: before=%d after=%d", len(snapshot), len(after))
	}
	for i := range snapshot {
		if snapshot[i] != after[i] {
			t.Errorf("row %d changed: before=%+v after=%+v", i, snapshot[i], after[i])
		}
	}

	// currentVersion should be unchanged.
	if v, _ := s.currentVersion(); v != snapshot[len(snapshot)-1].version {
		t.Errorf("currentVersion after re-migrate = %d, want %d", v, snapshot[len(snapshot)-1].version)
	}

	// Third call for paranoia.
	if err := s.migrate(); err != nil {
		t.Fatalf("third migrate: %v", err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_version`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != len(snapshot) {
		t.Errorf("row count after third migrate = %d, want %d", n, len(snapshot))
	}
}

func TestCurrentVersion_FreshDatabaseIsZero(t *testing.T) {
	// A Store whose schema_version table doesn't exist yet should
	// report version 0, not an error. (This is the path new installs
	// hit before the first migration runs.)
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	s := &Store{db: db}
	v, err := s.currentVersion()
	if err != nil {
		t.Fatalf("currentVersion on fresh DB: %v", err)
	}
	if v != 0 {
		t.Errorf("currentVersion on fresh DB = %d, want 0", v)
	}
}
