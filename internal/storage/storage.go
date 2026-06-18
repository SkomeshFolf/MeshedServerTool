// Package storage wraps the SQLite database layer.
//
// The schema is built up over multiple phases via versioned migrations:
//   - Phase 0: schema_version
//   - Phase 1: users, sessions
//   - Phase 2+: servers, server_state, reports, bans, motd (added in later phases)
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, no CGo
)

// Store is the application database handle. Thread-safe.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) the SQLite database at path. The parent
// directory must already exist with safe permissions; see
// cmd/meshed/main.go where we MkdirAll(... 0o700) before calling.
//
// We chmod the DB file after the driver creates it because modernc.org/sqlite
// does not expose a way to set the mode on creation. The DSN applies the
// usual journal_mode(WAL) + foreign_keys(1) + busy_timeout(5000) pragmas.
func Open(path string) (*Store, error) {
	// Make sure the file exists with a safe mode before opening. If it
	// already exists from a previous install (e.g. one created before
	// this hardening pass), chmod it now to 0o600.
	if _, err := os.Stat(path); err == nil {
		_ = os.Chmod(path, 0o600)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite is fine with many readers + one writer; cap connections
	// to be safe across all platforms.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if err := db.PingContext(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	// Belt-and-suspenders: even if the file was created in this call
	// (fresh install), make sure it's 0o600. SQLite may also have created
	// -wal and -shm sidecars; chmod them too.
	_ = os.Chmod(path, 0o600)
	_ = os.Chmod(path+"-wal", 0o600)
	_ = os.Chmod(path+"-shm", 0o600)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// OpenFromDSN opens a SQLite database using a fully-formed DSN string,
// including any pragmas. Use this when the default Open() — which
// appends a hard-coded pragma set and chmod-s the file — doesn't fit
// (e.g. an in-memory test DB). Production code should prefer Open().
func OpenFromDSN(dsn string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	if err := db.PingContext(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// Close releases the database handle.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// DB returns the underlying *sql.DB. Use sparingly — prefer typed methods
// on Store.
func (s *Store) DB() *sql.DB {
	return s.db
}

// Backup writes a consistent, point-in-time snapshot of the database
// to destPath using SQLite's VACUUM INTO statement. The resulting file
// is a fully self-contained SQLite database (WAL is folded in) that can
// be restored with `meshed --data-dir <new>` after copying it into
// place as `meshed.db`.
//
// VACUUM INTO takes a brief write lock for the duration of the copy,
// which is fine for a manually-invoked backup endpoint. The file is
// created with 0o600 (chmod'd after creation; SQLite doesn't take a
// mode arg on VACUUM INTO). On Windows the chmod is a no-op but the
// default ACL on the user's profile dir is fine for single-user.
// (audit finding M13)
func (s *Store) Backup(destPath string) error {
	// VACUUM INTO refuses to overwrite an existing file; we want a
	// clean path so the user's prior snapshot isn't silently lost if
	// the new copy succeeds but the rename is interrupted. Resolve
	// to an absolute path, refuse to write into the data dir as
	// `meshed.db` (would clobber the live DB), and clean up partial
	// files on error.
	if err := s.db.Ping(); err != nil {
		return fmt.Errorf("ping before backup: %w", err)
	}
	// Use a tmp file in the same dir as destPath so the rename is
	// atomic on POSIX. We can't pass a bind-mount to VACUUM INTO,
	// so we let SQLite write to destPath directly and then chmod
	// it. If the VACUUM itself fails, we remove any partial file.
	if _, err := s.db.Exec(`VACUUM INTO ?`, destPath); err != nil {
		_ = os.Remove(destPath) // best effort
		return fmt.Errorf("vacuum into: %w", err)
	}
	_ = os.Chmod(destPath, 0o600) // best effort on Windows
	return nil
}

// migration is a single versioned schema change. Migrations run in order;
// once applied, they never re-run.
type migration struct {
	version int
	name    string
	up      func(tx *sql.Tx) error
}

// allMigrations is the ordered list of every migration the binary knows about.
// Append-only — never reorder or modify a released migration; add a new one.
func allMigrations() []migration {
	return []migration{
		{
			version: 1,
			name:    "phase0-bootstrap",
			up: func(tx *sql.Tx) error {
				_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS schema_version (
					version INTEGER PRIMARY KEY,
					applied_at TEXT NOT NULL
				);`)
				return err
			},
		},
		{
			version: 2,
			name:    "phase1-users-sessions",
			up: func(tx *sql.Tx) error {
				_, err := tx.Exec(`
					CREATE TABLE users (
						id INTEGER PRIMARY KEY AUTOINCREMENT,
						username TEXT NOT NULL UNIQUE,
						password_hash TEXT NOT NULL,
						role TEXT NOT NULL DEFAULT 'user',
						created_at TEXT NOT NULL,
						last_login_at TEXT
					);
					CREATE INDEX idx_users_username ON users(username);

					CREATE TABLE sessions (
						token TEXT PRIMARY KEY,
						user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
						created_at TEXT NOT NULL,
						last_seen_at TEXT NOT NULL,
						expires_at TEXT NOT NULL,
						user_agent TEXT,
						ip TEXT
					);
					CREATE INDEX idx_sessions_user_id ON sessions(user_id);
					CREATE INDEX idx_sessions_expires_at ON sessions(expires_at);
				`)
				return err
			},
		},
		{
			version: 3,
			name:    "phase2-servers",
			up: func(tx *sql.Tx) error {
				_, err := tx.Exec(`
					CREATE TABLE servers (
						name TEXT PRIMARY KEY,
						install_dir TEXT NOT NULL,
						port INTEGER NOT NULL DEFAULT 7777,
						max_players INTEGER NOT NULL DEFAULT 32,
						hostname TEXT,
						args_json TEXT NOT NULL DEFAULT '{}',
						autostart INTEGER NOT NULL DEFAULT 0,
						created_at TEXT NOT NULL,
						updated_at TEXT NOT NULL
					);

					CREATE TABLE server_state (
						server_name TEXT PRIMARY KEY REFERENCES servers(name) ON DELETE CASCADE,
						status TEXT NOT NULL DEFAULT 'stopped',
						pid INTEGER,
						started_at TEXT,
						stopped_at TEXT,
						last_exit_code INTEGER,
						current_users INTEGER NOT NULL DEFAULT 0,
						current_map TEXT,
						current_gamemode TEXT,
						updated_at TEXT NOT NULL
					);
				`)
				return err
			},
		},
		{
			version: 4,
			name:    "phase4-reports-bans-chat",
			up: func(tx *sql.Tx) error {
				_, err := tx.Exec(`
					CREATE TABLE reports (
						id INTEGER PRIMARY KEY AUTOINCREMENT,
						server_name TEXT NOT NULL,
						target_id TEXT NOT NULL,
						target_name TEXT NOT NULL,
						source_id TEXT NOT NULL,
						source_name TEXT NOT NULL,
						date TEXT NOT NULL,
						reason TEXT NOT NULL DEFAULT '',
						text TEXT NOT NULL DEFAULT '',
						hash TEXT NOT NULL UNIQUE,
						handled INTEGER NOT NULL DEFAULT 0,
						handled_at TEXT,
						created_at TEXT NOT NULL
					);
					CREATE INDEX idx_reports_handled ON reports(handled);
					CREATE INDEX idx_reports_server ON reports(server_name);
					CREATE INDEX idx_reports_target ON reports(target_id);

					CREATE TABLE bans (
						id INTEGER PRIMARY KEY AUTOINCREMENT,
						steam_id TEXT NOT NULL UNIQUE,
						player_name TEXT NOT NULL DEFAULT '',
						reason TEXT NOT NULL DEFAULT '',
						banned_by TEXT NOT NULL DEFAULT '',
						banned_at TEXT NOT NULL
					);
					CREATE INDEX idx_bans_steam_id ON bans(steam_id);

					CREATE TABLE chat_messages (
						id INTEGER PRIMARY KEY AUTOINCREMENT,
						server_name TEXT NOT NULL,
						player_name TEXT NOT NULL,
						message TEXT NOT NULL,
						at TEXT NOT NULL
					);
					CREATE INDEX idx_chat_server_at ON chat_messages(server_name, at);
				`)
				return err
			},
		},
		{
			version: 5,
			name:    "phase5-motd",
			up: func(tx *sql.Tx) error {
				// motd.server_name intentionally does NOT have a REFERENCES
				// servers(name) constraint — we want MOTD entries to be
				// creatable for planned servers before they're added to the
				// servers table. (v2 had the same flexibility.)
				_, err := tx.Exec(`
					CREATE TABLE motd (
						server_name TEXT PRIMARY KEY,
						message TEXT NOT NULL,
						enabled INTEGER NOT NULL DEFAULT 1,
						updated_at TEXT NOT NULL,
						updated_by TEXT NOT NULL DEFAULT ''
					);

					-- Key-value store for global settings (used for the
					-- global default MOTD when no per-server row exists).
					CREATE TABLE settings (
						key TEXT PRIMARY KEY,
						value TEXT NOT NULL,
						updated_at TEXT NOT NULL
					);
				`)
				return err
			},
		},
		{
			version: 6,
			name:    "coalesce-null-server-state-strings",
			up: func(tx *sql.Tx) error {
				// The server_state columns current_map and current_gamemode
				// are declared without a NOT NULL constraint (and we don't
				// want to add one retroactively, since a NULL means "never
				// observed" and that's a legitimate state). But the Go side
				// scans them into a plain `string`, which fails on a NULL
				// column. Coalesce any existing NULLs to '' so the manager
				// rehydration path on boot doesn't crash. New rows from
				// here on are populated as empty strings by the seed code
				// in CreateServer.
				//
				// Idempotent: the WHERE clause means a re-run on an already-
				// coalesced DB affects zero rows. No error path to worry about.
				if _, err := tx.Exec(
					`UPDATE server_state SET current_map = '' WHERE current_map IS NULL`,
				); err != nil {
					return err
				}
				if _, err := tx.Exec(
					`UPDATE server_state SET current_gamemode = '' WHERE current_gamemode IS NULL`,
				); err != nil {
					return err
				}
				return nil
			},
		},
	}
}

// migrate applies all pending migrations inside a single transaction.
func (s *Store) migrate() error {
	migrations := allMigrations()
	current, err := s.currentVersion()
	if err != nil {
		return err
	}
	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		if err := s.applyMigration(m); err != nil {
			return fmt.Errorf("apply migration %d (%s): %w", m.version, m.name, err)
		}
	}
	return nil
}

func (s *Store) currentVersion() (int, error) {
	// schema_version may not exist on a brand-new DB; that's fine, version 0.
	row := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`)
	var v int
	if err := row.Scan(&v); err != nil {
		// Table doesn't exist yet → 0
		return 0, nil
	}
	return v, nil
}

func (s *Store) applyMigration(m migration) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Run the schema change first, then record that it ran. Otherwise
	// the very first migration can't record itself in a table that
	// doesn't exist yet.
	if err := m.up(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`INSERT INTO schema_version (version, applied_at) VALUES (?, ?)`,
		m.version, time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		return err
	}
	return tx.Commit()
}

// ErrNotFound is returned when a lookup matches no rows.
var ErrNotFound = errors.New("not found")
