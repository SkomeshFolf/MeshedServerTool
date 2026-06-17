// Package storage wraps the SQLite database layer.
//
// In Phase 0 this is a thin pass-through that owns the *sql.DB handle.
// Phase 1 will add real schema and query methods. Returning a real
// *sql.DB-backed Store from Open keeps the wiring honest from day one
// so we don't refactor the call site later.
package storage

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, no CGo
)

// Store is the application database handle. Thread-safe.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) the SQLite database at path and runs migrations.
//
// Migrations are no-ops until Phase 1; this just verifies the driver works.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := db.Ping(); err != nil {
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
// on Store once they exist.
func (s *Store) DB() *sql.DB {
	return s.db
}

// migrate runs all schema migrations. Idempotent.
func (s *Store) migrate() error {
	// Phase 0: just create a tiny version table so we can prove migrations run.
	// Phase 1 will replace this with real schema.
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_version (
			version INTEGER PRIMARY KEY
		);
	`)
	return err
}
