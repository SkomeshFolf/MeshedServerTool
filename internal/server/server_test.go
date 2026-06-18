package server

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Skomesh/MeshedServerTool/internal/chat"
	"github.com/Skomesh/MeshedServerTool/internal/hub"
	"github.com/Skomesh/MeshedServerTool/internal/logs"
	"github.com/Skomesh/MeshedServerTool/internal/storage"
)

// newTestManager opens an in-memory storage, runs migrations, and
// constructs a Manager wired to it. The DB is closed during t.Cleanup.
//
// We use NewManager (not a manual wire-up) so the test exercises the
// same rehydration path the production binary does.
func newTestManager(t *testing.T) (*Manager, *storage.Store, *hub.Hub) {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "server-test.db") +
		"?mode=memory&cache=shared&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"

	s, err := storage.OpenFromDSN(dsn)
	if err != nil {
		t.Fatalf("storage.OpenFromDSN: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	h := hub.NewHub()
	t.Cleanup(func() { h.Close() })

	cs := chat.New(s)
	m, err := NewManager(s, h, cs)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m, s, h
}

// addTestServer registers a server in the DB and adds it to the
// manager. The default args map points at `/bin/sh -c "..."` so the
// caller can supply a shell snippet to act as the fake game binary.
func addTestServer(t *testing.T, m *Manager, s *storage.Store, name, shellCmd string) *storage.Server {
	t.Helper()
	ctx := context.Background()
	srv := &storage.Server{
		Name:       name,
		InstallDir: t.TempDir(),
		Port:       0,
		MaxPlayers: 8,
		Args: map[string]any{
			"executable": "/bin/sh",
			"argv":       []any{"-c", shellCmd},
		},
	}
	if err := s.Servers().CreateServer(ctx, srv); err != nil {
		t.Fatalf("CreateServer: %v", err)
	}
	m.AddServer(srv)
	return srv
}

// waitFor polls cond every 10ms until it returns true or timeout
// elapses. Returns true if the condition was met, false on timeout.
func waitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// requireShell skips the test if /bin/sh isn't available (Windows).
func requireShell(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("/bin/sh"); err != nil {
		t.Skip("/bin/sh not available on this platform")
	}
}

// writeCloser is an io.WriteCloser backed by a function. The Close
// method is a no-op — we use this in tests that exercise the
// serialization of WriteStdin without going through a real process.
type writeCloser struct {
	w func(p []byte) (int, error)
}

func (wc *writeCloser) Write(p []byte) (int, error) { return wc.w(p) }
func (wc *writeCloser) Close() error                { return nil }

// TestPumpPipe_LinesAreBuffered verifies that pumpPipe correctly
// reads lines from a source, tags them with the stream name, and
// appends them to the buffer. We use a strings.Reader as the source.
func TestPumpPipe_LinesAreBuffered(t *testing.T) {
	t.Parallel()
	buf := logs.NewBuffer(100)
	src := strings.NewReader("alpha\nbeta\ngamma\n")
	pumpPipe(src, buf, "stdout")

	lines := buf.Snapshot(10)
	if got, want := len(lines), 3; got != want {
		t.Fatalf("got %d lines, want %d", got, want)
	}
	wantTexts := []string{"alpha", "beta", "gamma"}
	for i, l := range lines {
		if l.Text != wantTexts[i] {
			t.Errorf("line %d: got %q, want %q", i, l.Text, wantTexts[i])
		}
		if l.Stream != "stdout" {
			t.Errorf("line %d: stream = %q, want %q", i, l.Stream, "stdout")
		}
	}
}

// TestPumpPipe_EmptyInput verifies the empty-source path: pumpPipe
// returns without error and appends nothing to the buffer.
func TestPumpPipe_EmptyInput(t *testing.T) {
	t.Parallel()
	buf := logs.NewBuffer(10)
	src := strings.NewReader("")
	pumpPipe(src, buf, "stdout")
	if got := buf.Snapshot(10); len(got) != 0 {
		t.Errorf("expected 0 lines, got %d", len(got))
	}
}

// TestWriteStdin_NilReturnsError verifies the error path when the
// process isn't running (stdin writer is nil).
func TestWriteStdin_NilReturnsError(t *testing.T) {
	t.Parallel()
	s := &Server{} // zero value, no stdin writer
	if err := s.WriteStdin([]byte("hello\n")); err == nil {
		t.Fatal("expected error writing to nil stdin, got nil")
	}
}

// TestWriteStdin_SerializesConcurrentWrites is a race-detector
// check that two goroutines calling WriteStdin don't interleave
// bytes in the same line. With the stdinMu lock held for the
// duration of the write, we should see "aaaa\nbbbb\n" or
// "bbbb\naaaa\n" — never a mixed run.
func TestWriteStdin_SerializesConcurrentWrites(t *testing.T) {
	t.Parallel()

	var (
		mu    sync.Mutex
		out   []byte
		wg    sync.WaitGroup
		ready sync.WaitGroup
	)
	ready.Add(2)
	wg.Add(2)
	fake := &writeCloser{w: func(p []byte) (int, error) {
		mu.Lock()
		out = append(out, p...)
		mu.Unlock()
		return len(p), nil
	}}
	s := &Server{stdin: fake}

	for i, payload := range []string{"aaaa\n", "bbbb\n"} {
		i, payload := i, payload
		go func() {
			defer wg.Done()
			ready.Done()
			ready.Wait() // start both goroutines simultaneously
			if err := s.WriteStdin([]byte(payload)); err != nil {
				t.Errorf("goroutine %d: %v", i, err)
			}
		}()
	}
	wg.Wait()

	mu.Lock()
	got := string(out)
	mu.Unlock()
	switch got {
	case "aaaa\nbbbb\n", "bbbb\naaaa\n":
		// OK
	default:
		t.Fatalf("interleaved writes: got %q (want one of the two non-interleaved forms)", got)
	}
}

// TestServer_StartAndStop is the integration check: spawn a real
// /bin/sh subprocess, see it become "running", capture some log
// output, then stop it and see it return to a terminal state.
func TestServer_StartAndStop(t *testing.T) {
	requireShell(t)
	m, s, _ := newTestManager(t)
	// The shell script prints three lines then sleeps forever.
	// We expect all three to be captured by the log buffer.
	addTestServer(t, m, s, "test-srv",
		"echo line-1; echo line-2; echo line-3; sleep 60")

	if err := m.Start(context.Background(), "test-srv"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for running state.
	if !waitFor(2*time.Second, func() bool {
		v := m.Get("test-srv")
		return v != nil && v.State.Status == storage.StatusRunning
	}) {
		t.Fatalf("server did not reach running state, got %s", m.Get("test-srv").State.Status)
	}

	// Wait for the buffer to receive all 3 lines.
	srv := m.ServerByName("test-srv")
	if !waitFor(2*time.Second, func() bool {
		return len(srv.LogBuffer().Snapshot(10)) >= 3
	}) {
		t.Fatalf("did not see 3 log lines, got %d", len(srv.LogBuffer().Snapshot(10)))
	}

	// Now stop with a 2s grace.
	if err := m.Stop(context.Background(), "test-srv", 2*time.Second); err != nil {
		t.Errorf("Stop: %v", err)
	}

	// Wait for terminal state.
	if !waitFor(3*time.Second, func() bool {
		v := m.Get("test-srv")
		return v != nil && (v.State.Status == storage.StatusStopped ||
			v.State.Status == storage.StatusCrashed)
	}) {
		t.Errorf("server did not reach terminal state, got %s", m.Get("test-srv").State.Status)
	}

	// PID should be cleared.
	if v := m.Get("test-srv"); v.State.PID != nil {
		t.Errorf("expected nil PID after stop, got %v", *v.State.PID)
	}
}

// TestServer_StartRejectsRunningState verifies the state-machine
// guard: starting a server that's already starting/running returns
// ErrInvalidTransition.
func TestServer_StartRejectsRunningState(t *testing.T) {
	requireShell(t)
	m, s, _ := newTestManager(t)
	addTestServer(t, m, s, "test-srv", "sleep 60")

	if err := m.Start(context.Background(), "test-srv"); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	// Wait for running, then try to start again.
	if !waitFor(2*time.Second, func() bool {
		v := m.Get("test-srv")
		return v != nil && v.State.Status == storage.StatusRunning
	}) {
		t.Fatal("server did not reach running state")
	}
	defer m.Stop(context.Background(), "test-srv", 2*time.Second)

	err := m.Start(context.Background(), "test-srv")
	if err != ErrInvalidTransition {
		t.Errorf("expected ErrInvalidTransition, got %v", err)
	}
}

// TestServer_StopRejectsStoppedState verifies the symmetric guard:
// stopping a server that isn't running returns ErrInvalidTransition.
func TestServer_StopRejectsStoppedState(t *testing.T) {
	t.Parallel()
	m, _, _ := newTestManager(t)
	// Register a server without starting it.
	m.AddServer(&storage.Server{
		Name:       "stopped-srv",
		InstallDir: t.TempDir(),
		Port:       0,
		MaxPlayers: 8,
		Args:       map[string]any{"executable": "/bin/sh"},
	})
	err := m.Stop(context.Background(), "stopped-srv", time.Second)
	if err != ErrInvalidTransition {
		t.Errorf("expected ErrInvalidTransition, got %v", err)
	}
}

// TestServer_Restart verifies the Restart path: stop, then start.
// After Restart, the server should be back to "running".
func TestServer_Restart(t *testing.T) {
	requireShell(t)
	m, s, _ := newTestManager(t)
	addTestServer(t, m, s, "restart-srv", "echo first; sleep 60")

	if err := m.Start(context.Background(), "restart-srv"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !waitFor(2*time.Second, func() bool {
		v := m.Get("restart-srv")
		return v != nil && v.State.Status == storage.StatusRunning
	}) {
		t.Fatal("server did not reach running state")
	}

	// Restart should stop + start. We expect a brief "stopping"
	// phase but ultimately running.
	if err := m.Restart(context.Background(), "restart-srv"); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if !waitFor(5*time.Second, func() bool {
		v := m.Get("restart-srv")
		return v != nil && v.State.Status == storage.StatusRunning
	}) {
		t.Errorf("server did not reach running after restart, got %s", m.Get("restart-srv").State.Status)
	}

	// Cleanup
	_ = m.Stop(context.Background(), "restart-srv", 2*time.Second)
}

// TestManager_AutostartHonored verifies that NewManager starts any
// server with autostart=true whose args map contains an executable.
//
// We seed the DB with a single autostart server BEFORE constructing
// the manager. NewManager's rehydration path will then see the
// autostart flag and call Start() in a goroutine.
//
// Note: the spawned /bin/sh process leaks until the test binary
// exits. That's acceptable for a test — the OS reaps it. We do
// reach for the manager in a separate call to start a managed
// cleanup so subsequent tests aren't impacted by the running shell.
func TestManager_AutostartHonored(t *testing.T) {
	requireShell(t)

	// Seed: open storage, create a server with autostart=true.
	dsn := "file:" + filepath.Join(t.TempDir(), "autostart-test.db") +
		"?mode=memory&cache=shared&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	store, err := storage.OpenFromDSN(dsn)
	if err != nil {
		t.Fatalf("OpenFromDSN: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	srv := &storage.Server{
		Name:       "autostart-srv",
		InstallDir: t.TempDir(),
		Port:       0,
		MaxPlayers: 8,
		Autostart:  true,
		Args: map[string]any{
			"executable": "/bin/sh",
			"argv":       []any{"-c", "echo AUTOSTART-RAN; sleep 60"},
		},
	}
	if err := store.Servers().CreateServer(ctx, srv); err != nil {
		t.Fatalf("CreateServer: %v", err)
	}

	h := hub.NewHub()
	t.Cleanup(func() { h.Close() })

	cs := chat.New(store)
	m, err := NewManager(store, h, cs)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() {
		_ = m.StopAll(context.Background(), 2*time.Second)
	})

	// Wait for the server to reach running state by polling the DB.
	if !waitFor(3*time.Second, func() bool {
		state, err := store.Servers().GetState(ctx, "autostart-srv")
		if err != nil {
			return false
		}
		return state.Status == storage.StatusRunning
	}) {
		state, _ := store.Servers().GetState(ctx, "autostart-srv")
		t.Fatalf("autostart server did not reach running, got %s", state.Status)
	}
}

// TestHub_PublishAndSubscribe is a sanity check for the hub that
// the manager depends on. It verifies that events published while a
// subscriber is attached are delivered to that subscriber's channel.
func TestHub_PublishAndSubscribe(t *testing.T) {
	t.Parallel()
	h := hub.NewHub()
	t.Cleanup(func() { h.Close() })

	sub := h.Subscribe()
	defer h.Unsubscribe(sub)

	var got atomic.Int32
	done := make(chan struct{})
	go func() {
		for range sub.C {
			got.Add(1)
			if got.Load() >= 2 {
				close(done)
				return
			}
		}
	}()

	h.Publish(hub.Event{Type: "test.event", Data: "first"})
	h.Publish(hub.Event{Type: "test.event", Data: "second"})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("only got %d/2 events", got.Load())
	}
}
