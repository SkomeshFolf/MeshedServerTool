package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Role represents a user's permission level.
type Role string

const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

// User mirrors a row in the users table.
type User struct {
	ID           int64
	Username     string
	PasswordHash string
	Role         Role
	CreatedAt    time.Time
	LastLoginAt  *time.Time
}

// Session mirrors a row in the sessions table.
type Session struct {
	Token       string
	UserID      int64
	CreatedAt   time.Time
	LastSeenAt  time.Time
	ExpiresAt   time.Time
	UserAgent   string
	IP          string
}

// UserStore provides typed CRUD over the users table.
type UserStore struct{ s *Store }

// SessionStore provides typed CRUD over the sessions table.
type SessionStore struct{ s *Store }

func (s *Store) Users() *UserStore     { return &UserStore{s: s} }
func (s *Store) Sessions() *SessionStore { return &SessionStore{s: s} }

// CountUsers returns the total number of users. Used to gate first-user bootstrap.
func (us *UserStore) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := us.s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// CreateFirstUserTx creates the very first user inside a transaction so
// that two concurrent bootstrap calls can't both succeed. Returns
// ErrNoUsers if any user already exists (or if a concurrent bootstrap
// already created one inside the same transaction).
func (us *UserStore) CreateFirstUserTx(ctx context.Context, username, passwordHash string, role Role) (*User, error) {
	tx, err := us.s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Re-check inside the transaction. With SetMaxOpenConns(1) writers are
	// serialized, so this is correct: the second concurrent call will block
	// on BeginTx and then see the inserted row.
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, ErrNoUsers
	}

	now := time.Now().UTC()
	res, err := tx.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, role, created_at) VALUES (?, ?, ?, ?)`,
		username, passwordHash, string(role), now.Format(time.RFC3339),
	)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &User{
		ID: id, Username: username, PasswordHash: passwordHash, Role: role,
		CreatedAt: now,
	}, nil
}

// ErrNoUsers signals a bootstrap attempt that found at least one user.
// Used by storage.UserStore.CreateFirstUserTx and auth.Service.CreateFirstUser.
var ErrNoUsers = errors.New("bootstrap not available")

// CreateUser inserts a new user. Username must be unique (enforced by DB).
func (us *UserStore) CreateUser(ctx context.Context, username, passwordHash string, role Role) (*User, error) {
	now := time.Now().UTC()
	res, err := us.s.db.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, role, created_at) VALUES (?, ?, ?, ?)`,
		username, passwordHash, string(role), now.Format(time.RFC3339),
	)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &User{
		ID: id, Username: username, PasswordHash: passwordHash, Role: role,
		CreatedAt: now,
	}, nil
}

// GetUserByUsername returns the user with the given username, or ErrNotFound.
func (us *UserStore) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	row := us.s.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash, role, created_at, last_login_at FROM users WHERE username = ?`,
		username,
	)
	return scanUser(row)
}

// GetUserByID returns the user with the given id, or ErrNotFound.
func (us *UserStore) GetUserByID(ctx context.Context, id int64) (*User, error) {
	row := us.s.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash, role, created_at, last_login_at FROM users WHERE id = ?`,
		id,
	)
	return scanUser(row)
}

// MarkLoggedIn updates the user's last_login_at timestamp.
func (us *UserStore) MarkLoggedIn(ctx context.Context, id int64) error {
	_, err := us.s.db.ExecContext(ctx,
		`UPDATE users SET last_login_at = ? WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339), id,
	)
	return err
}

func scanUser(row *sql.Row) (*User, error) {
	var u User
	var role, createdAt string
	var lastLogin sql.NullString
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &role, &createdAt, &lastLogin); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	u.Role = Role(role)
	if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
		u.CreatedAt = t
	}
	if lastLogin.Valid {
		if t, err := time.Parse(time.RFC3339, lastLogin.String); err == nil {
			u.LastLoginAt = &t
		}
	}
	return &u, nil
}

// CreateSession inserts a new session row.
func (ss *SessionStore) CreateSession(ctx context.Context, sess *Session) error {
	_, err := ss.s.db.ExecContext(ctx,
		`INSERT INTO sessions (token, user_id, created_at, last_seen_at, expires_at, user_agent, ip)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sess.Token, sess.UserID,
		sess.CreatedAt.UTC().Format(time.RFC3339),
		sess.LastSeenAt.UTC().Format(time.RFC3339),
		sess.ExpiresAt.UTC().Format(time.RFC3339),
		sess.UserAgent, sess.IP,
	)
	return err
}

// GetSession returns the session with the given token, or ErrNotFound.
func (ss *SessionStore) GetSession(ctx context.Context, token string) (*Session, error) {
	row := ss.s.db.QueryRowContext(ctx,
		`SELECT token, user_id, created_at, last_seen_at, expires_at, user_agent, ip
		 FROM sessions WHERE token = ?`,
		token,
	)
	var sess Session
	var createdAt, lastSeen, expires string
	var ua, ip sql.NullString
	if err := row.Scan(&sess.Token, &sess.UserID, &createdAt, &lastSeen, &expires, &ua, &ip); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
		sess.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339, lastSeen); err == nil {
		sess.LastSeenAt = t
	}
	if t, err := time.Parse(time.RFC3339, expires); err == nil {
		sess.ExpiresAt = t
	}
	if ua.Valid {
		sess.UserAgent = ua.String
	}
	if ip.Valid {
		sess.IP = ip.String
	}
	return &sess, nil
}

// TouchSession updates last_seen_at to now. Cheap; runs on every authenticated
// request so we can prune stale sessions later.
func (ss *SessionStore) TouchSession(ctx context.Context, token string) error {
	_, err := ss.s.db.ExecContext(ctx,
		`UPDATE sessions SET last_seen_at = ? WHERE token = ?`,
		time.Now().UTC().Format(time.RFC3339), token,
	)
	return err
}

// DeleteSession removes a session (logout).
func (ss *SessionStore) DeleteSession(ctx context.Context, token string) error {
	_, err := ss.s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token = ?`, token)
	return err
}

// PurgeExpired removes all sessions whose expires_at is in the past.
func (ss *SessionStore) PurgeExpired(ctx context.Context) (int64, error) {
	res, err := ss.s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at < ?`,
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
