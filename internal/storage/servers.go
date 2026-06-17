package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Status represents the runtime state of a server.
type Status string

const (
	StatusStopped  Status = "stopped"
	StatusStarting Status = "starting"
	StatusRunning  Status = "running"
	StatusStopping Status = "stopping"
	StatusCrashed  Status = "crashed"
)

// Server is the user-managed configuration for a game server.
type Server struct {
	Name       string `json:"name"`
	InstallDir string `json:"install_dir"`
	Port       int    `json:"port"`
	MaxPlayers int    `json:"max_players"`
	Hostname   string `json:"hostname"`
	Args       map[string]any `json:"args"`
	Autostart  bool   `json:"autostart"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// ServerState is the runtime state, separate from config. Updated by the
// server manager as the process lifecycle progresses.
type ServerState struct {
	ServerName       string `json:"server_name"`
	Status           Status `json:"status"`
	PID              *int64 `json:"pid,omitempty"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	StoppedAt        *time.Time `json:"stopped_at,omitempty"`
	LastExitCode     *int    `json:"last_exit_code,omitempty"`
	CurrentUsers     int     `json:"current_users"`
	CurrentMap       string  `json:"current_map"`
	CurrentGamemode  string  `json:"current_gamemode"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// ServerView is what the API returns: server config + current state joined.
type ServerView struct {
	Server
	State ServerState `json:"state"`
}

// ServerStore provides typed CRUD over servers + server_state.
type ServerStore struct{ s *Store }

func (s *Store) Servers() *ServerStore { return &ServerStore{s: s} }

// CreateServer inserts a new server with an initial state row.
func (ss *ServerStore) CreateServer(ctx context.Context, srv *Server) error {
	now := time.Now().UTC()
	srv.CreatedAt = now
	srv.UpdatedAt = now
	if srv.Args == nil {
		srv.Args = map[string]any{}
	}
	argsJSON, err := json.Marshal(srv.Args)
	if err != nil {
		return err
	}
	_, err = ss.s.db.ExecContext(ctx, `
		INSERT INTO servers (name, install_dir, port, max_players, hostname, args_json, autostart, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		srv.Name, srv.InstallDir, srv.Port, srv.MaxPlayers, srv.Hostname,
		string(argsJSON), boolToInt(srv.Autostart),
		srv.CreatedAt.Format(time.RFC3339), srv.UpdatedAt.Format(time.RFC3339),
	)
	if err != nil {
		return err
	}
	// Seed the state row so subsequent UPDATEs have something to write to.
	_, err = ss.s.db.ExecContext(ctx, `
		INSERT INTO server_state (server_name, status, updated_at) VALUES (?, ?, ?)`,
		srv.Name, string(StatusStopped), now.Format(time.RFC3339),
	)
	return err
}

// GetServer fetches one server by name. Returns ErrNotFound if absent.
func (ss *ServerStore) GetServer(ctx context.Context, name string) (*Server, error) {
	row := ss.s.db.QueryRowContext(ctx,
		`SELECT name, install_dir, port, max_players, hostname, args_json, autostart, created_at, updated_at
		 FROM servers WHERE name = ?`, name)
	return scanServer(row)
}

// ListServers returns all servers (no pagination; a single install is
// expected to have a handful at most).
func (ss *ServerStore) ListServers(ctx context.Context) ([]*Server, error) {
	rows, err := ss.s.db.QueryContext(ctx,
		`SELECT name, install_dir, port, max_players, hostname, args_json, autostart, created_at, updated_at
		 FROM servers ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Server
	for rows.Next() {
		s, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// UpdateServerConfig updates the mutable config fields. Name is the PK
// and can't change. Args is replaced wholesale.
func (ss *ServerStore) UpdateServerConfig(ctx context.Context, srv *Server) error {
	now := time.Now().UTC()
	srv.UpdatedAt = now
	if srv.Args == nil {
		srv.Args = map[string]any{}
	}
	argsJSON, err := json.Marshal(srv.Args)
	if err != nil {
		return err
	}
	res, err := ss.s.db.ExecContext(ctx, `
		UPDATE servers SET install_dir=?, port=?, max_players=?, hostname=?, args_json=?, autostart=?, updated_at=?
		WHERE name=?`,
		srv.InstallDir, srv.Port, srv.MaxPlayers, srv.Hostname,
		string(argsJSON), boolToInt(srv.Autostart),
		srv.UpdatedAt.Format(time.RFC3339), srv.Name,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteServer removes a server and its state (FK CASCADE).
func (ss *ServerStore) DeleteServer(ctx context.Context, name string) error {
	res, err := ss.s.db.ExecContext(ctx, `DELETE FROM servers WHERE name = ?`, name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetState returns the runtime state for a server.
func (ss *ServerStore) GetState(ctx context.Context, name string) (*ServerState, error) {
	row := ss.s.db.QueryRowContext(ctx,
		`SELECT server_name, status, pid, started_at, stopped_at, last_exit_code,
		 current_users, current_map, current_gamemode, updated_at
		 FROM server_state WHERE server_name = ?`, name)
	return scanState(row)
}

// ListStates returns all states. Used to rebuild the in-memory manager
// at startup.
func (ss *ServerStore) ListStates(ctx context.Context) ([]*ServerState, error) {
	rows, err := ss.s.db.QueryContext(ctx,
		`SELECT server_name, status, pid, started_at, stopped_at, last_exit_code,
		 current_users, current_map, current_gamemode, updated_at
		 FROM server_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ServerState
	for rows.Next() {
		st, err := scanState(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// UpdateStateStatus changes just the status field. Used by the manager
// during lifecycle transitions.
func (ss *ServerStore) UpdateStateStatus(ctx context.Context, name string, status Status, pid *int64, startedAt, stoppedAt *time.Time, lastExitCode *int) error {
	now := time.Now().UTC()
	res, err := ss.s.db.ExecContext(ctx, `
		UPDATE server_state SET status=?, pid=?, started_at=?, stopped_at=?, last_exit_code=?, updated_at=?
		WHERE server_name=?`,
		string(status), int64PtrToNull(pid),
		formatTimePtr(startedAt), formatTimePtr(stoppedAt), intPtrToNull(lastExitCode),
		now.Format(time.RFC3339), name,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateStateLiveFields updates the live-runtime fields (current_users,
// current_map, current_gamemode) without touching status/pid.
func (ss *ServerStore) UpdateStateLiveFields(ctx context.Context, name string, users int, mapName, gamemode string) error {
	now := time.Now().UTC()
	_, err := ss.s.db.ExecContext(ctx, `
		UPDATE server_state SET current_users=?, current_map=?, current_gamemode=?, updated_at=?
		WHERE server_name=?`,
		users, mapName, gamemode, now.Format(time.RFC3339), name,
	)
	return err
}

// scanner lets scanServer/scanState accept either *sql.Row or *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanServer(r scanner) (*Server, error) {
	var s Server
	var argsJSON string
	var autostart int
	var createdAt, updatedAt string
	if err := r.Scan(&s.Name, &s.InstallDir, &s.Port, &s.MaxPlayers,
		&s.Hostname, &argsJSON, &autostart, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
		s.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339, updatedAt); err == nil {
		s.UpdatedAt = t
	}
	s.Autostart = autostart != 0
	if argsJSON != "" {
		_ = json.Unmarshal([]byte(argsJSON), &s.Args)
	}
	return &s, nil
}

func scanState(r scanner) (*ServerState, error) {
	var st ServerState
	var status string
	var pid sql.NullInt64
	var startedAt, stoppedAt sql.NullString
	var lastExit sql.NullInt64
	var updatedAt string
	if err := r.Scan(&st.ServerName, &status, &pid, &startedAt, &stoppedAt,
		&lastExit, &st.CurrentUsers, &st.CurrentMap, &st.CurrentGamemode, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	st.Status = Status(status)
	if pid.Valid {
		v := pid.Int64
		st.PID = &v
	}
	if lastExit.Valid {
		v := int(lastExit.Int64)
		st.LastExitCode = &v
	}
	if startedAt.Valid {
		if t, err := time.Parse(time.RFC3339, startedAt.String); err == nil {
			st.StartedAt = &t
		}
	}
	if stoppedAt.Valid {
		if t, err := time.Parse(time.RFC3339, stoppedAt.String); err == nil {
			st.StoppedAt = &t
		}
	}
	if t, err := time.Parse(time.RFC3339, updatedAt); err == nil {
		st.UpdatedAt = t
	}
	return &st, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func int64PtrToNull(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func intPtrToNull(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func formatTimePtr(p *time.Time) any {
	if p == nil {
		return nil
	}
	return p.UTC().Format(time.RFC3339)
}
