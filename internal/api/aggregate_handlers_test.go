package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Skomesh/MeshedServerTool/internal/chat"
	"github.com/Skomesh/MeshedServerTool/internal/hub"
	"github.com/Skomesh/MeshedServerTool/internal/logs"
	"github.com/Skomesh/MeshedServerTool/internal/server"
	"github.com/Skomesh/MeshedServerTool/internal/storage"
)

func newTestAggregateEnv(t *testing.T) (*v1AggregateDeps, *server.Manager, *storage.Store) {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "agg-test.db") +
		"?mode=memory&cache=shared&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	store, err := storage.OpenFromDSN(dsn)
	if err != nil {
		t.Fatalf("OpenFromDSN: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	h := hub.NewHub()
	t.Cleanup(func() { h.Close() })
	cs := chat.New(store)
	mgr, err := server.NewManager(store, h, cs)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return &v1AggregateDeps{manager: mgr, store: store}, mgr, store
}

// addAggregateServer registers a server (DB + manager) and returns it.
// It does NOT call Start — we only need the manager to know about it
// for AllServers() and LogBuffer().
func addAggregateServer(t *testing.T, mgr *server.Manager, store *storage.Store, name string) {
	t.Helper()
	srv := &storage.Server{
		Name:       name,
		InstallDir: t.TempDir(),
		Port:       0,
		MaxPlayers: 8,
		Args: map[string]any{
			"executable": "/bin/sh",
			"argv":       []any{"-c", "sleep 0.1"},
		},
	}
	if err := store.Servers().CreateServer(context.Background(), srv); err != nil {
		t.Fatalf("CreateServer: %v", err)
	}
	mgr.AddServer(srv)
}

// callAgg dispatches to the right handler based on path prefix.
func callAgg(t *testing.T, d *v1AggregateDeps, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, nil)
	rr := httptest.NewRecorder()
	if path == "/logs" || path == "/logs/" || (len(path) >= 5 && path[:5] == "/logs") {
		d.handleAggregateLogs(rr, r)
	} else {
		d.handleAggregateChats(rr, r)
	}
	return rr
}

func TestAggregate_Logs_Empty(t *testing.T) {
	t.Parallel()
	d, mgr, _ := newTestAggregateEnv(t)
	addAggregateServer(t, mgr, d.store, "alpha")
	addAggregateServer(t, mgr, d.store, "beta")
	rr := callAgg(t, d, http.MethodGet, "/logs")
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var resp struct {
		Entries []LogEntry `json:"entries"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if resp.Entries == nil || len(resp.Entries) != 0 {
		t.Errorf("expected empty entries, got %v", resp.Entries)
	}
}

func TestAggregate_Logs_FilterByServer(t *testing.T) {
	t.Parallel()
	d, mgr, _ := newTestAggregateEnv(t)
	addAggregateServer(t, mgr, d.store, "alpha")
	addAggregateServer(t, mgr, d.store, "beta")
	// Push a log line into alpha's buffer.
	var alpha *server.Server
	for _, s := range mgr.AllServers() {
		if s.Config().Name == "alpha" {
			alpha = s
			break
		}
	}
	if alpha == nil {
		t.Fatal("alpha not in manager")
	}
	alpha.LogBuffer().Append(logs.Line{
		Text: "alpha line 1",
		Type: "raw",
		At:   time.Now().UTC(),
	})
	rr := callAgg(t, d, http.MethodGet, "/logs?server=alpha")
	var resp struct {
		Entries []LogEntry `json:"entries"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if len(resp.Entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(resp.Entries))
	}
	if len(resp.Entries) > 0 && resp.Entries[0].ServerName != "alpha" {
		t.Errorf("server_name: got %q, want alpha", resp.Entries[0].ServerName)
	}
}

func TestAggregate_Logs_TailParam(t *testing.T) {
	t.Parallel()
	d, mgr, _ := newTestAggregateEnv(t)
	addAggregateServer(t, mgr, d.store, "alpha")
	var alpha *server.Server
	for _, s := range mgr.AllServers() {
		if s.Config().Name == "alpha" {
			alpha = s
			break
		}
	}
	for i := 0; i < 10; i++ {
		alpha.LogBuffer().Append(logs.Line{
			Text: "line " + strconv.Itoa(i),
			Type: "raw",
			At:   time.Now().UTC(),
		})
	}
	// tail=3 → only 3 most recent.
	rr := callAgg(t, d, http.MethodGet, "/logs?tail=3")
	var resp struct {
		Entries []LogEntry `json:"entries"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if len(resp.Entries) != 3 {
		t.Errorf("expected 3 entries, got %d", len(resp.Entries))
	}
}

func TestAggregate_Logs_MethodNotAllowed(t *testing.T) {
	t.Parallel()
	d, _, _ := newTestAggregateEnv(t)
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		rr := callAgg(t, d, m, "/logs")
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s /logs: got %d, want 405", m, rr.Code)
		}
	}
}

func TestAggregate_Chats_FilterByServer(t *testing.T) {
	t.Parallel()
	d, mgr, store := newTestAggregateEnv(t)
	addAggregateServer(t, mgr, store, "alpha")
	addAggregateServer(t, mgr, store, "beta")
	cs := chat.New(store)
	if err := cs.Add(context.Background(), "alpha", "p1", "msg1"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := cs.Add(context.Background(), "beta", "p2", "msg2"); err != nil {
		t.Fatalf("add: %v", err)
	}
	rr := callAgg(t, d, http.MethodGet, "/chats?server=alpha")
	var resp struct {
		Entries []ChatEntry `json:"entries"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if len(resp.Entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(resp.Entries))
	}
	if len(resp.Entries) > 0 && resp.Entries[0].ServerName != "alpha" {
		t.Errorf("server_name: got %q, want alpha", resp.Entries[0].ServerName)
	}
}

func TestAggregate_Chats_Since(t *testing.T) {
	t.Parallel()
	d, mgr, store := newTestAggregateEnv(t)
	addAggregateServer(t, mgr, store, "alpha")
	cs := chat.New(store)
	if err := cs.Add(context.Background(), "alpha", "p1", "msg1"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := cs.Add(context.Background(), "alpha", "p2", "msg2"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := cs.Add(context.Background(), "alpha", "p3", "msg3"); err != nil {
		t.Fatalf("add: %v", err)
	}
	// List all first to find the middle message's id.
	rr := callAgg(t, d, http.MethodGet, "/chats")
	var resp struct {
		Entries []ChatEntry `json:"entries"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if len(resp.Entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(resp.Entries))
	}
	// Entries are newest first; pick the middle (index 1) and use its ID
	// as `since` — we should get 1 entry (the newest, with higher id).
	midID := resp.Entries[1].ID
	rr = callAgg(t, d, http.MethodGet, "/chats?since="+strconv.FormatInt(midID, 10))
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if len(resp.Entries) != 1 {
		t.Errorf("expected 1 entry (newer than since=%d), got %d", midID, len(resp.Entries))
	}
}

func TestAggregate_Chats_Limit(t *testing.T) {
	t.Parallel()
	d, mgr, store := newTestAggregateEnv(t)
	addAggregateServer(t, mgr, store, "alpha")
	cs := chat.New(store)
	for i := 0; i < 5; i++ {
		if err := cs.Add(context.Background(), "alpha", "p", "m"); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	rr := callAgg(t, d, http.MethodGet, "/chats?limit=2")
	var resp struct {
		Entries []ChatEntry `json:"entries"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if len(resp.Entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(resp.Entries))
	}
}

func TestAggregate_Chats_Empty(t *testing.T) {
	t.Parallel()
	d, mgr, _ := newTestAggregateEnv(t)
	addAggregateServer(t, mgr, d.store, "alpha")
	rr := callAgg(t, d, http.MethodGet, "/chats")
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var resp struct {
		Entries []ChatEntry `json:"entries"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if resp.Entries == nil || len(resp.Entries) != 0 {
		t.Errorf("expected empty array, got %v", resp.Entries)
	}
}

func TestAggregate_Chats_MethodNotAllowed(t *testing.T) {
	t.Parallel()
	d, _, _ := newTestAggregateEnv(t)
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		rr := callAgg(t, d, m, "/chats")
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s /chats: got %d, want 405", m, rr.Code)
		}
	}
}
