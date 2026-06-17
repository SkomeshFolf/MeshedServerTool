// Package bans is the global ban list.
//
// In v2, bans lived in a flat `banlist.txt` file that was copied to
// each server's `BannedIDs.ini`. v3 keeps the same data model — the
// bans table is the source of truth, and a BannedIDs.ini is generated
// for each server on demand. Phase 4 ships the database and the API;
// the per-server INI sync happens on a hook the manager calls when
// the ban list changes (so a live running game picks up new bans).
package bans

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Skomesh/MeshedServerTool/internal/storage"
)

// Ban is one entry in the global ban list.
type Ban struct {
	ID         int64     `json:"id"`
	SteamID    string    `json:"steam_id"`
	PlayerName string    `json:"player_name"`
	Reason     string    `json:"reason"`
	BannedBy   string    `json:"banned_by"`
	BannedAt   time.Time `json:"banned_at"`
}

// Store is the typed CRUD for bans.
type Store struct{ s *storage.Store }

// New returns a Store backed by the given connection.
func New(s *storage.Store) *Store { return &Store{s: s} }

// Add inserts a new ban. If the SteamID is already banned, updates the
// existing record (player_name, reason, banned_by) — matching v2's
// behavior of re-banning just refreshing the row.
func (bs *Store) Add(ctx context.Context, steamID, playerName, reason, bannedBy string) (*Ban, error) {
	now := time.Now().UTC()
	_, err := bs.s.DB().ExecContext(ctx, `
		INSERT INTO bans (steam_id, player_name, reason, banned_by, banned_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(steam_id) DO UPDATE SET
			player_name = excluded.player_name,
			reason = excluded.reason,
			banned_by = excluded.banned_by,
			banned_at = excluded.banned_at`,
		steamID, playerName, reason, bannedBy, now.Format(time.RFC3339),
	)
	if err != nil {
		return nil, err
	}
	return bs.GetBySteamID(ctx, steamID)
}

// GetBySteamID returns the ban for the given SteamID.
func (bs *Store) GetBySteamID(ctx context.Context, steamID string) (*Ban, error) {
	row := bs.s.DB().QueryRowContext(ctx,
		`SELECT id, steam_id, player_name, reason, banned_by, banned_at FROM bans WHERE steam_id = ?`, steamID)
	return scanBan(row)
}

// GetByID returns the ban with the given id.
func (bs *Store) GetByID(ctx context.Context, id int64) (*Ban, error) {
	row := bs.s.DB().QueryRowContext(ctx,
		`SELECT id, steam_id, player_name, reason, banned_by, banned_at FROM bans WHERE id = ?`, id)
	return scanBan(row)
}

// List returns all bans, newest first.
func (bs *Store) List(ctx context.Context) ([]*Ban, error) {
	rows, err := bs.s.DB().QueryContext(ctx,
		`SELECT id, steam_id, player_name, reason, banned_by, banned_at FROM bans ORDER BY banned_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Ban
	for rows.Next() {
		b, err := scanBan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Remove deletes a ban.
func (bs *Store) Remove(ctx context.Context, id int64) error {
	res, err := bs.s.DB().ExecContext(ctx, `DELETE FROM bans WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return storage.ErrNotFound
	}
	return nil
}

// ListSteamIDs returns just the SteamID column, in banned_at DESC order.
// Used by the per-server BannedIDs.ini sync.
func (bs *Store) ListSteamIDs(ctx context.Context) ([]string, error) {
	rows, err := bs.s.DB().QueryContext(ctx,
		`SELECT steam_id FROM bans ORDER BY banned_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

type scanner interface{ Scan(dest ...any) error }

func scanBan(r scanner) (*Ban, error) {
	var b Ban
	var bannedAt string
	if err := r.Scan(&b.ID, &b.SteamID, &b.PlayerName, &b.Reason, &b.BannedBy, &bannedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	if t, err := time.Parse(time.RFC3339, bannedAt); err == nil {
		b.BannedAt = t
	}
	return &b, nil
}
