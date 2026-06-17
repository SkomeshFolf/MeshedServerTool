// Package motd is the per-server message-of-the-day store, with a
// global fallback default.
//
// The model: a per-server row in `motd` overrides the global default
// stored as `settings.key='motd.default'`. If a server has no row,
// the default applies. If the default is empty, no MOTD is shown.
//
// In v3, MOTD is shown in the React UI as a banner. v2 displayed
// it on game join via SCP: 5k's MOTD system; that side of the wire
// is still owned by the game server itself — this store just
// manages the text.
package motd

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Skomesh/MeshedServerTool/internal/storage"
)

// MOTD is one per-server or global default message.
type MOTD struct {
	ServerName string    `json:"server_name,omitempty"`
	Message    string    `json:"message"`
	Enabled    bool      `json:"enabled"`
	UpdatedAt  time.Time `json:"updated_at"`
	UpdatedBy  string    `json:"updated_by"`
}

// Store is the typed CRUD for MOTD entries and global settings.
type Store struct{ s *storage.Store }

// New returns a Store backed by the given connection.
func New(s *storage.Store) *Store { return &Store{s: s} }

// GetForServer returns the effective MOTD for a server — the per-server
// row if it exists, otherwise the global default.
//
// The boolean reports whether the result came from a per-server override
// (true) or the global default (false). The UI uses this to label the
// banner correctly.
func (ms *Store) GetForServer(ctx context.Context, serverName string) (*MOTD, bool, error) {
	// Try per-server first.
	row := ms.s.DB().QueryRowContext(ctx,
		`SELECT server_name, message, enabled, updated_at, updated_by
		 FROM motd WHERE server_name = ?`, serverName)
	m := &MOTD{}
	var enabled int
	var updatedAt string
	if err := row.Scan(&m.ServerName, &m.Message, &enabled, &updatedAt, &m.UpdatedBy); err == nil {
		m.Enabled = enabled != 0
		if t, err := time.Parse(time.RFC3339, updatedAt); err == nil {
			m.UpdatedAt = t
		}
		return m, true, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	// Fall back to global default.
	g, err := ms.GetGlobal(ctx)
	if err != nil {
		return nil, false, err
	}
	if g == nil {
		return &MOTD{Message: "", Enabled: false}, false, nil
	}
	g.ServerName = serverName // surface server context even on default
	return g, false, nil
}

// GetGlobal returns the global default MOTD (settings key "motd.default"),
// or nil if unset.
func (ms *Store) GetGlobal(ctx context.Context) (*MOTD, error) {
	row := ms.s.DB().QueryRowContext(ctx,
		`SELECT value, updated_at FROM settings WHERE key = ?`, "motd.default")
	var value, updatedAt string
	if err := row.Scan(&value, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &MOTD{
		Message:   value,
		Enabled:   true, // if it's set in settings, it's enabled
		UpdatedAt: parseTime(updatedAt),
	}, nil
}

// SetForServer writes a per-server MOTD. Creates or replaces the row.
// `enabled=false` clears the per-server entry (effectively unsetting the
// override) without deleting it — easier to audit later.
func (ms *Store) SetForServer(ctx context.Context, serverName, message string, enabled bool, updatedBy string) (*MOTD, error) {
	now := time.Now().UTC()
	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	_, err := ms.s.DB().ExecContext(ctx, `
		INSERT INTO motd (server_name, message, enabled, updated_at, updated_by)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(server_name) DO UPDATE SET
			message = excluded.message,
			enabled = excluded.enabled,
			updated_at = excluded.updated_at,
			updated_by = excluded.updated_by`,
		serverName, message, enabledInt, now.Format(time.RFC3339), updatedBy,
	)
	if err != nil {
		return nil, err
	}
	m, _, err := ms.GetForServer(ctx, serverName)
	return m, err
}

// SetGlobal sets the global default MOTD.
func (ms *Store) SetGlobal(ctx context.Context, message, updatedBy string) (*MOTD, error) {
	now := time.Now().UTC()
	_, err := ms.s.DB().ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			value = excluded.value,
			updated_at = excluded.updated_at`,
		"motd.default", message, now.Format(time.RFC3339),
	)
	if err != nil {
		return nil, err
	}
	return &MOTD{
		Message:    message,
		Enabled:    true,
		UpdatedAt:  now,
		UpdatedBy:  updatedBy,
		ServerName: "*", // sentinel for "global"
	}, nil
}

// DeleteForServer removes a per-server MOTD override.
func (ms *Store) DeleteForServer(ctx context.Context, serverName string) error {
	res, err := ms.s.DB().ExecContext(ctx, `DELETE FROM motd WHERE server_name = ?`, serverName)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return storage.ErrNotFound
	}
	return nil
}

// List returns all per-server MOTD entries.
func (ms *Store) List(ctx context.Context) ([]*MOTD, error) {
	rows, err := ms.s.DB().QueryContext(ctx,
		`SELECT server_name, message, enabled, updated_at, updated_by
		 FROM motd ORDER BY server_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*MOTD
	for rows.Next() {
		m := &MOTD{}
		var enabled int
		var updatedAt string
		if err := rows.Scan(&m.ServerName, &m.Message, &enabled, &updatedAt, &m.UpdatedBy); err != nil {
			return nil, err
		}
		m.Enabled = enabled != 0
		m.UpdatedAt = parseTime(updatedAt)
		out = append(out, m)
	}
	return out, rows.Err()
}

func parseTime(s string) time.Time {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Time{}
}
