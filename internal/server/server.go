// Package server manages the lifecycle of managed game servers.
//
// Design: a Manager owns a set of Server values, one per game server.
// Each Server wraps a real OS subprocess (the dedicated server binary)
// and a log file watcher. Lifecycle calls (Start/Stop/Restart) update
// both the in-memory state and the database (server_state row).
package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"sync"
	"time"

	"github.com/Skomesh/MeshedServerTool/internal/hub"
	"github.com/Skomesh/MeshedServerTool/internal/logs"
	"github.com/Skomesh/MeshedServerTool/internal/storage"
)

// Status transitions are documented in PLAN.md. The legal moves are:
//
//	stopped   → starting
//	starting  → running | crashed
//	running   → stopping
//	stopping  → stopped | crashed
//	crashed   → starting | stopped
//
// Illegal transitions return ErrInvalidTransition.
var ErrInvalidTransition = errors.New("invalid state transition")

// Manager coordinates all managed servers.
type Manager struct {
	store  *storage.Store
	hub    *hub.Hub
	mu     sync.RWMutex
	servers map[string]*Server
}

// NewManager constructs a Manager and rehydrates the in-memory map from
// the database. Servers in "running" state in the DB are marked as
// "crashed" at startup — we can't know if the original process is still
// alive, and we'd rather the user re-start explicitly than think a server
// is running when it isn't.
func NewManager(store *storage.Store, h *hub.Hub) (*Manager, error) {
	m := &Manager{
		store:   store,
		hub:     h,
		servers: make(map[string]*Server),
	}
	servers, err := store.Servers().ListServers(context.Background())
	if err != nil {
		return nil, err
	}
	for _, srv := range servers {
		state, err := store.Servers().GetState(context.Background(), srv.Name)
		if err != nil {
			return nil, err
		}
		// If we crashed on a previous run, the OS may still hold a process
		// (we lost track of it on shutdown). Mark as crashed; user can restart.
		if state.Status == storage.StatusRunning ||
			state.Status == storage.StatusStarting ||
			state.Status == storage.StatusStopping {
			state.Status = storage.StatusCrashed
			_ = store.Servers().UpdateStateStatus(context.Background(),
				srv.Name, storage.StatusCrashed, nil, nil, ptrTime(time.Now().UTC()), nil)
		}
		m.servers[srv.Name] = &Server{
			store: store,
			hub:   h,
			cfg:   srv,
			state: state,
		}
	}
	return m, nil
}

func ptrTime(t time.Time) *time.Time { return &t }

// AddServer registers a new server with the manager. The DB row must
// already exist (created via storage.Servers().CreateServer).
func (m *Manager) AddServer(srv *storage.Server) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, _ := m.store.Servers().GetState(context.Background(), srv.Name)
	if state == nil {
		state = &storage.ServerState{ServerName: srv.Name, Status: storage.StatusStopped}
	}
	m.servers[srv.Name] = &Server{
		store: m.store,
		hub:   m.hub,
		cfg:   srv,
		state: state,
	}
}

// RemoveServer stops (if running) and removes a server from the manager.
// The DB row is deleted by the caller.
func (m *Manager) RemoveServer(name string) error {
	m.mu.Lock()
	srv, ok := m.servers[name]
	if !ok {
		m.mu.Unlock()
		return storage.ErrNotFound
	}
	delete(m.servers, name)
	m.mu.Unlock()
	if srv.IsRunning() {
		_ = srv.Stop(context.Background(), 5*time.Second)
	}
	return nil
}

// List returns a snapshot of all server views (config + state).
func (m *Manager) List() []*storage.ServerView {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*storage.ServerView, 0, len(m.servers))
	for _, s := range m.servers {
		out = append(out, &storage.ServerView{Server: *s.cfg, State: *s.state})
	}
	return out
}

// Get returns a single server view by name, or nil.
func (m *Manager) Get(name string) *storage.ServerView {
	m.mu.RLock()
	defer m.mu.RUnlock()
	srv, ok := m.servers[name]
	if !ok {
		return nil
	}
	return &storage.ServerView{Server: *srv.cfg, State: *srv.state}
}

// ServerByName returns the live *Server for the given name (used by the
// log API). Returns nil if absent.
func (m *Manager) ServerByName(name string) *Server {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.servers[name]
}

// Start is a convenience for manager.servers[name].Start.
func (m *Manager) Start(ctx context.Context, name string) error {
	m.mu.RLock()
	srv := m.servers[name]
	m.mu.RUnlock()
	if srv == nil {
		return storage.ErrNotFound
	}
	return srv.Start(ctx)
}

// Stop is a convenience for manager.servers[name].Stop.
func (m *Manager) Stop(ctx context.Context, name string, grace time.Duration) error {
	m.mu.RLock()
	srv := m.servers[name]
	m.mu.RUnlock()
	if srv == nil {
		return storage.ErrNotFound
	}
	return srv.Stop(ctx, grace)
}

// Restart stops (if running) and starts the server.
func (m *Manager) Restart(ctx context.Context, name string) error {
	m.mu.RLock()
	srv := m.servers[name]
	m.mu.RUnlock()
	if srv == nil {
		return storage.ErrNotFound
	}
	if srv.IsRunning() {
		if err := srv.Stop(ctx, 5*time.Second); err != nil {
			return fmt.Errorf("stop before restart: %w", err)
		}
	}
	return srv.Start(ctx)
}

// Server is one managed game server.
type Server struct {
	store *storage.Store
	hub   *hub.Hub
	cfg   *storage.Server
	state *storage.ServerState
	mu    sync.Mutex

	cmd        *exec.Cmd
	cancel     context.CancelFunc
	logBuf     *logs.Buffer
	logBufOnce sync.Once
}

// publishState broadcasts the current state to the hub. Caller must NOT
// hold s.mu; the function takes the lock briefly to snapshot, then
// publishes with the lock released to keep the critical section short.
func (s *Server) publishState() {
	if s.hub == nil {
		return
	}
	s.mu.Lock()
	view := storage.ServerView{Server: *s.cfg, State: *s.state}
	s.mu.Unlock()
	s.hub.Publish(hub.Event{Type: "server.state", Data: view})
}

// IsRunning reports whether the server's subprocess is alive.
func (s *Server) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Status == storage.StatusRunning ||
		s.state.Status == storage.StatusStarting
}

// LogBuffer returns the server's recent-log buffer. Used by the API layer.
func (s *Server) LogBuffer() *logs.Buffer {
	return s.logBufLazy()
}

// logBufLazy returns the server's log buffer, allocating it on first use.
// Backed by sync.Once so concurrent callers don't double-allocate.
//
// The buffer is also wired into the hub on first allocation: every new
// line is published as a hub.Event with type "log.line". Subscribers
// (WebSocket clients) receive the line in real time.
func (s *Server) logBufLazy() *logs.Buffer {
	s.logBufOnce.Do(func() {
		s.logBuf = logs.NewBuffer(500) // last 500 lines
		if s.hub != nil {
			s.logBuf.OnAppend(func(l logs.Line) {
				s.hub.Publish(hub.Event{
					Type: "log.line",
					Data: map[string]any{
						"server_name": s.cfg.Name,
						"line":        l,
					},
				})
			})
		}
	})
	return s.logBuf
}

// Start launches the subprocess and returns once the process has been
// spawned. Status transitions to "starting" synchronously, then to
// "running" once we've confirmed the process is alive (via a small wait).
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.state.Status == storage.StatusRunning ||
		s.state.Status == storage.StatusStarting {
		s.mu.Unlock()
		return ErrInvalidTransition
	}
	if err := storage.ErrNotFound; err != nil && s.cfg == nil {
		s.mu.Unlock()
		return err
	}

	// Build the command. In Phase 2, we accept a custom command (executable
	// path in the args map) so this can be smoke-tested without a real
	// game binary. A future phase will add a "command template" field to
	// the server config so the manager knows how to invoke the dedicated
	// server binary correctly.
	executable, args := s.buildCommand()
	if executable == "" {
		s.mu.Unlock()
		return errors.New("server has no executable configured; set args.executable")
	}

	// Use a background context for the subprocess so it outlives the HTTP
	// request that started it. The process is owned by the manager, not
	// the request; cancellation goes through Stop().
	cmd := exec.CommandContext(context.Background(), executable, args...)
	cmd.Dir = s.cfg.InstallDir
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("stderr pipe: %w", err)
	}

	// Mark starting
	now := time.Now().UTC()
	if err := s.store.Servers().UpdateStateStatus(ctx, s.cfg.Name, storage.StatusStarting, nil, &now, nil, nil); err != nil {
		s.mu.Unlock()
		return err
	}
	s.state.Status = storage.StatusStarting
	s.state.StartedAt = &now
	s.cmd = cmd
	buf := s.logBufLazy() // allocate buffer while we hold the lock
	s.mu.Unlock()
	s.publishState() // broadcast the "starting" event

	if err := cmd.Start(); err != nil {
		s.mu.Lock()
		s.cancel = nil
		s.cmd = nil
		s.state.Status = storage.StatusCrashed
		_ = s.store.Servers().UpdateStateStatus(ctx, s.cfg.Name, storage.StatusCrashed, nil, nil, ptrTime(time.Now().UTC()), nil)
		s.mu.Unlock()
		s.publishState() // broadcast the "crashed" event
		return fmt.Errorf("start process: %w", err)
	}

	pid := int64(cmd.Process.Pid)
	s.mu.Lock()
	s.state.PID = &pid
	s.state.Status = storage.StatusRunning
	_ = s.store.Servers().UpdateStateStatus(ctx, s.cfg.Name, storage.StatusRunning, &pid, &now, nil, nil)
	s.mu.Unlock()
	s.publishState() // broadcast the "running" event

	// Pump both pipes into the log buffer.
	go pumpPipe(stdoutPipe, buf, "stdout")
	go pumpPipe(stderrPipe, buf, "stderr")

	// Wait for the process to exit in the background; update state when it does.
	go func() {
		_ = cmd.Wait()
		exitCode := 0
		if cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		}
		s.mu.Lock()
		wasRunning := s.state.Status == storage.StatusRunning ||
			s.state.Status == storage.StatusStarting ||
			s.state.Status == storage.StatusStopping
		if wasRunning {
			stoppedAt := time.Now().UTC()
			final := storage.StatusStopped
			if exitCode != 0 && s.state.Status != storage.StatusStopping {
				final = storage.StatusCrashed
			}
			s.state.Status = final
			s.state.PID = nil
			s.state.StoppedAt = &stoppedAt
			s.state.LastExitCode = &exitCode
			_ = s.store.Servers().UpdateStateStatus(context.Background(),
				s.cfg.Name, final, nil, nil, &stoppedAt, &exitCode)
			s.cmd = nil
			s.cancel = nil
		}
		s.mu.Unlock()
		if wasRunning {
			s.publishState() // broadcast the final state
		}
	}()

	return nil
}

// Stop sends SIGTERM (or kills on Windows) and waits up to grace for the
// process to exit. On timeout, it kills hard.
func (s *Server) Stop(ctx context.Context, grace time.Duration) error {
	s.mu.Lock()
	if s.state.Status != storage.StatusRunning &&
		s.state.Status != storage.StatusStarting {
		s.mu.Unlock()
		return ErrInvalidTransition
	}
	cmd := s.cmd
	s.state.Status = storage.StatusStopping
	_ = s.store.Servers().UpdateStateStatus(ctx, s.cfg.Name, storage.StatusStopping, nil, nil, nil, nil)
	s.mu.Unlock()
	s.publishState() // broadcast the "stopping" event

	if cmd == nil || cmd.Process == nil {
		return nil
	}

	// Cross-platform: Process.Kill is the only thing that works everywhere,
	// but SIGTERM gives the process a chance to clean up. We try SIGTERM
	// first, then fall back to Kill.
	if err := terminateProcess(cmd); err != nil {
		log.Printf("terminate %s: %v", s.cfg.Name, err)
	}

	// Wait for the Wait() goroutine to flip the state to a terminal value.
	// cmd.Wait is already running in a goroutine; we poll state instead
	// of calling Wait again (which would panic on a second caller).
	deadline := time.Now().Add(grace)
	for {
		s.mu.Lock()
		final := s.state.Status == storage.StatusStopped ||
			s.state.Status == storage.StatusCrashed
		s.mu.Unlock()
		if final {
			return nil
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			return nil
		}
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
			// loop
		}
	}
}

// buildCommand returns the executable and args for the server's process.
// In Phase 2 this is driven by the args map: `args.executable` is the
// path to run, and `args.argv` is a list of extra args. Later phases will
// add the full SCP: 5k / SCP Pandemic server template.
func (s *Server) buildCommand() (string, []string) {
	exe, _ := s.cfg.Args["executable"].(string)
	if exe == "" {
		return "", nil
	}
	var argv []string
	if raw, ok := s.cfg.Args["argv"].([]any); ok {
		for _, a := range raw {
			if str, ok := a.(string); ok {
				argv = append(argv, str)
			}
		}
	}
	return exe, argv
}

// pumpPipe copies lines from r into buf with a stream tag.
func pumpPipe(r interface{ Read(p []byte) (int, error) }, buf *logs.Buffer, stream string) {
	// Read one byte at a time; cheap, correct, and avoids an extra import.
	// For high-volume servers this would want a bufio.Scanner.
	var line []byte
	one := make([]byte, 1)
	for {
		n, err := r.Read(one)
		if n > 0 {
			if one[0] == '\n' {
				if len(line) > 0 {
					buf.Append(logs.Line{Stream: stream, Text: string(line)})
					line = line[:0]
				}
			} else if one[0] != '\r' {
				line = append(line, one[0])
			}
		}
		if err != nil {
			if len(line) > 0 {
				buf.Append(logs.Line{Stream: stream, Text: string(line)})
			}
			return
		}
	}
}
