// Package reports handles player reports: storage, detection from log
// lines, and the read/ban actions exposed to the UI.
//
// In v2, reports came from the game's `<install>/Reports/` directory
// where each file was a structured text blob. v3 keeps the data shape
// (target/source names+ids, date, reason, text) but stores it in the
// reports table. Detection can be triggered two ways:
//
//   - From log lines: when a "report_notice" type is parsed, the line's
//     `details` field is treated as a one-line summary and we synthesize
//     a report. (The v2 game writes full report files, not log lines,
//     so this is opportunistic.)
//   - From an external source: the API accepts POST /reports with a
//     full report body. Future work can add a directory watcher for
//     `<install>/Reports/` like v2 did.
package reports

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Skomesh/MeshedServerTool/internal/storage"
)

// Report is the in-memory representation of a reports row.
type Report struct {
	ID         int64      `json:"id"`
	ServerName string     `json:"server_name"`
	TargetID   string     `json:"target_id"`
	TargetName string     `json:"target_name"`
	SourceID   string     `json:"source_id"`
	SourceName string     `json:"source_name"`
	Date       string     `json:"date"`
	Reason     string     `json:"reason"`
	Text       string     `json:"text"`
	Hash       string     `json:"hash"`
	Handled    bool       `json:"handled"`
	HandledAt  *time.Time `json:"handled_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Store is the typed CRUD for reports.
type Store struct{ s *storage.Store }

// New returns a Store backed by the given connection.
func New(s *storage.Store) *Store { return &Store{s: s} }

// Hash computes the v2-compatible MD5 hash from the report fields.
// v2 used MD5 — we keep it for compatibility with the de-dup logic
// the existing UI/scripts may rely on. (Not a security boundary.)
func Hash(target, targetID, source, sourceID, date, reason, text string) string {
	// Hash de-dupes identical reports. Use NUL separators between fields
	// so a field boundary can never be ambiguous (e.g. ("a","bc",...) and
	// ("ab","c",...) used to produce the same hash).
	h := md5.Sum([]byte(strings.Join([]string{
		target, targetID, source, sourceID, date, reason, text,
	}, "\x00")))
	return hex.EncodeToString(h[:])
}

// CreateReport inserts a new report. If a report with the same hash
// already exists, returns the existing one with exists=true and no error.
// This de-dup behavior matches v2.
func (rs *Store) CreateReport(ctx context.Context, r *Report) (created *Report, exists bool, err error) {
	now := time.Now().UTC()
	r.CreatedAt = now
	if r.Hash == "" {
		r.Hash = Hash(r.TargetName, r.TargetID, r.SourceName, r.SourceID, r.Date, r.Reason, r.Text)
	}
	_, err = rs.s.DB().ExecContext(ctx, `
		INSERT INTO reports (server_name, target_id, target_name, source_id, source_name,
		                    date, reason, text, hash, handled, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?)`,
		r.ServerName, r.TargetID, r.TargetName, r.SourceID, r.SourceName,
		r.Date, r.Reason, r.Text, r.Hash, r.CreatedAt.Format(time.RFC3339),
	)
	if err != nil {
		// Likely UNIQUE constraint on hash. Fetch the existing one.
		if isUniqueErr(err) {
			existing, gerr := rs.GetByHash(ctx, r.Hash)
			if gerr != nil {
				return nil, false, gerr
			}
			return existing, true, nil
		}
		return nil, false, err
	}
	// Fetch back so we have the assigned ID.
	got, err := rs.GetByHash(ctx, r.Hash)
	if err != nil {
		return nil, false, err
	}
	return got, false, nil
}

func isUniqueErr(err error) bool {
	// modernc.org/sqlite returns error strings containing "UNIQUE constraint"
	return err != nil && (containsAny(err.Error(), "UNIQUE constraint", "constraint failed: UNIQUE"))
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && len(s) >= len(sub) && indexOf(s, sub) >= 0 {
			return true
		}
	}
	return false
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// GetByHash returns the report with the given hash, or storage.ErrNotFound.
func (rs *Store) GetByHash(ctx context.Context, hash string) (*Report, error) {
	row := rs.s.DB().QueryRowContext(ctx, selectReportsSQL+" WHERE hash = ?", hash)
	return scanReport(row)
}

// GetByID returns the report with the given id, or storage.ErrNotFound.
func (rs *Store) GetByID(ctx context.Context, id int64) (*Report, error) {
	row := rs.s.DB().QueryRowContext(ctx, selectReportsSQL+" WHERE id = ?", id)
	return scanReport(row)
}

// List returns reports, optionally filtered.
//
//	handled == nil → all reports
//	handled != nil → only that handled state
//	serverName == "" → all servers
//	serverName != "" → that server
func (rs *Store) List(ctx context.Context, handled *bool, serverName string, limit int) ([]*Report, error) {
	q := selectReportsSQL + " WHERE 1=1"
	args := []any{}
	if handled != nil {
		if *handled {
			q += " AND handled = 1"
		} else {
			q += " AND handled = 0"
		}
	}
	if serverName != "" {
		q += " AND server_name = ?"
		args = append(args, serverName)
	}
	q += " ORDER BY created_at DESC"
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := rs.s.DB().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Report
	for rows.Next() {
		r, err := scanReport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkHandled marks a report as handled (or unhandled if `handled` is false).
func (rs *Store) MarkHandled(ctx context.Context, id int64, handled bool) error {
	now := time.Now().UTC()
	var handledAt any
	if handled {
		handledAt = now.Format(time.RFC3339)
	} else {
		handledAt = nil
	}
	res, err := rs.s.DB().ExecContext(ctx,
		`UPDATE reports SET handled = ?, handled_at = ? WHERE id = ?`,
		boolInt(handled), handledAt, id,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return storage.ErrNotFound
	}
	return nil
}

// Delete removes a report.
func (rs *Store) Delete(ctx context.Context, id int64) error {
	res, err := rs.s.DB().ExecContext(ctx, `DELETE FROM reports WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return storage.ErrNotFound
	}
	return nil
}

// CountByTarget returns the number of reports each target SteamID has.
// Matches v2's "reports per user" concept.
func (rs *Store) CountByTarget(ctx context.Context) (map[string]int, error) {
	rows, err := rs.s.DB().QueryContext(ctx,
		`SELECT target_id, COUNT(*) FROM reports GROUP BY target_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int)
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

const selectReportsSQL = `
SELECT id, server_name, target_id, target_name, source_id, source_name,
       date, reason, text, hash, handled, handled_at, created_at
FROM reports`

type scanner interface{ Scan(dest ...any) error }

func scanReport(r scanner) (*Report, error) {
	var rep Report
	var handled int
	var handledAt sql.NullString
	var createdAt string
	if err := r.Scan(&rep.ID, &rep.ServerName, &rep.TargetID, &rep.TargetName,
		&rep.SourceID, &rep.SourceName, &rep.Date, &rep.Reason, &rep.Text,
		&rep.Hash, &handled, &handledAt, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}
	rep.Handled = handled != 0
	if handledAt.Valid {
		if t, err := time.Parse(time.RFC3339, handledAt.String); err == nil {
			rep.HandledAt = &t
		}
	}
	if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
		rep.CreatedAt = t
	}
	return &rep, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
