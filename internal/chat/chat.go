// Package chat stores chat messages parsed from server logs.
//
// v2 had its own chat log directory; v3 keeps the data in SQLite so
// the UI can show history beyond the in-memory log buffer's 500 lines.
//
// The logs.Buffer's OnAppend hook in internal/server is wired so every
// "chat" or "chat_simple" type line is also persisted here.
package chat

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Skomesh/MeshedServerTool/internal/storage"
)

// Message is one chat line.
type Message struct {
	ID         int64     `json:"id"`
	ServerName string    `json:"server_name"`
	PlayerName string    `json:"player_name"`
	Message    string    `json:"message"`
	At         time.Time `json:"at"`
}

// Store is the typed CRUD for chat messages.
type Store struct{ s *storage.Store }

// New returns a Store backed by the given connection.
func New(s *storage.Store) *Store { return &Store{s: s} }

// Add appends a chat message. Used by the server manager's OnAppend
// hook whenever a chat-typed line arrives.
func (cs *Store) Add(ctx context.Context, serverName, playerName, message string) error {
	_, err := cs.s.DB().ExecContext(ctx,
		`INSERT INTO chat_messages (server_name, player_name, message, at) VALUES (?, ?, ?, ?)`,
		serverName, playerName, message, time.Now().UTC().Format(time.RFC3339))
	return err
}

// List returns recent messages for a server, newest first.
func (cs *Store) List(ctx context.Context, serverName string, limit int) ([]*Message, error) {
	if limit <= 0 || limit > 10000 {
		limit = 500
	}
	rows, err := cs.s.DB().QueryContext(ctx,
		`SELECT id, server_name, player_name, message, at FROM chat_messages
		 WHERE server_name = ? ORDER BY at DESC LIMIT ?`,
		serverName, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// PurgeOlderThan removes messages older than the given duration. Used
// for periodic cleanup; not exposed in the API for Phase 4.
func (cs *Store) PurgeOlderThan(ctx context.Context, d time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-d).Format(time.RFC3339)
	res, err := cs.s.DB().ExecContext(ctx,
		`DELETE FROM chat_messages WHERE at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

type scanner interface{ Scan(dest ...any) error }

func scanMessage(r scanner) (*Message, error) {
	var m Message
	var at string
	if err := r.Scan(&m.ID, &m.ServerName, &m.PlayerName, &m.Message, &at); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	if t, err := time.Parse(time.RFC3339, at); err == nil {
		m.At = t
	}
	return &m, nil
}
