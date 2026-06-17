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
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, no CGo
)

// Store is the application database handle. Thread-safe.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) the SQLite database at path and runs all
// pending migrations.
func Open(path string) (*Store, error) {
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
