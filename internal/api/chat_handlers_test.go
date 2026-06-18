package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Skomesh/MeshedServerTool/internal/chat"
	"github.com/Skomesh/MeshedServerTool/internal/storage"
)

func newTestChatHandlerEnv(t *testing.T) *v1ChatDeps {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "chat-handler-test.db") +
		"?mode=memory&cache=shared&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	store, err := storage.OpenFromDSN(dsn)
	if err != nil {
		t.Fatalf("OpenFromDSN: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &v1ChatDeps{store: chat.New(store)}
}

func (d *v1ChatDeps) callChat(t *testing.T, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, nil)
	rr := httptest.NewRecorder()
	d.ServeHTTP(rr, r)
	return rr
}

func TestChat_List_Empty(t *testing.T) {
	t.Parallel()
	d := newTestChatHandlerEnv(t)
	rr := d.callChat(t, http.MethodGet, "/alpha")
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var resp struct {
		Messages []*chat.Message `json:"messages"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if resp.Messages == nil || len(resp.Messages) != 0 {
		t.Errorf("expected empty array, got %v", resp.Messages)
	}
}

func TestChat_List_AfterAdds(t *testing.T) {
	t.Parallel()
	d := newTestChatHandlerEnv(t)
	// Insert directly via the store; the HTTP API is read-only.
	ctx := t.Context()
	for i, name := range []string{"alpha", "alpha", "beta"} {
		_ = i
		if err := d.store.Add(ctx, name, "player"+name, "msg"); err != nil {
			t.Fatalf("add: %v", err)
		}
		time.Sleep(time.Millisecond) // ensure ordering by `at`
	}
	rr := d.callChat(t, http.MethodGet, "/alpha")
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var resp struct {
		Messages []*chat.Message `json:"messages"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if len(resp.Messages) != 2 {
		t.Errorf("expected 2 alpha messages, got %d", len(resp.Messages))
	}
	for _, m := range resp.Messages {
		if m.ServerName != "alpha" {
			t.Errorf("wrong server_name: %q", m.ServerName)
		}
	}
}

func TestChat_List_LimitRespected(t *testing.T) {
	t.Parallel()
	d := newTestChatHandlerEnv(t)
	ctx := t.Context()
	for i := 0; i < 5; i++ {
		if err := d.store.Add(ctx, "alpha", "p", "m"); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	rr := d.callChat(t, http.MethodGet, "/alpha?limit=2")
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var resp struct {
		Messages []*chat.Message `json:"messages"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if len(resp.Messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(resp.Messages))
	}
}

func TestChat_List_DefaultLimit(t *testing.T) {
	t.Parallel()
	d := newTestChatHandlerEnv(t)
	ctx := t.Context()
	for i := 0; i < 3; i++ {
		if err := d.store.Add(ctx, "alpha", "p", "m"); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	// No limit param → default 200.
	rr := d.callChat(t, http.MethodGet, "/alpha")
	var resp struct {
		Messages []*chat.Message `json:"messages"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if len(resp.Messages) != 3 {
		t.Errorf("expected 3 messages, got %d", len(resp.Messages))
	}
}

func TestChat_MethodNotAllowed(t *testing.T) {
	t.Parallel()
	d := newTestChatHandlerEnv(t)
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		rr := d.callChat(t, m, "/alpha")
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s /alpha: got %d, want 405", m, rr.Code)
		}
	}
}

func TestChat_EmptyServerName(t *testing.T) {
	t.Parallel()
	d := newTestChatHandlerEnv(t)
	// /api/v1/chat/ (trailing slash, empty name).
	rr := d.callChat(t, http.MethodGet, "/")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rr.Code)
	}
}
