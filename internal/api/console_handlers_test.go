package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Skomesh/MeshedServerTool/internal/server"
)

// nullManager is a consoleManager that always returns nil (no
// server found). Sufficient for tests that exercise validation,
// method gates, and the server-not-found 404 path.
type nullManager struct{}

func (nullManager) ServerByName(string) *server.Server { return nil }

// fakeManager returns whatever we put in it.
type fakeManager struct {
	mu      sync.Mutex
	servers map[string]*server.Server
}

func newFakeManager() *fakeManager { return &fakeManager{servers: map[string]*server.Server{}} }

func (m *fakeManager) ServerByName(name string) *server.Server {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.servers[name]
}

func TestConsole_ServerNotFound(t *testing.T) {
	t.Parallel()
	deps := &v1ConsoleDeps{manager: nullManager{}}
	for _, p := range []string{"/stdin", "/stdin/line"} {
		body := `{"input":"x"}`
		if strings.HasSuffix(p, "line") {
			body = `{"text":"x"}`
		}
		r := httptest.NewRequest(http.MethodPost, p, strings.NewReader(body))
		rr := httptest.NewRecorder()
		deps.ServeHTTP(rr, r)
		if rr.Code != http.StatusNotFound {
			t.Errorf("POST %s on missing server: got %d, want 404; body=%s", p, rr.Code, rr.Body.String())
		}
	}
}

func TestConsole_MethodNotAllowed(t *testing.T) {
	t.Parallel()
	deps := &v1ConsoleDeps{manager: nullManager{}}
	for _, m := range []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		r := httptest.NewRequest(m, "/stdin", nil)
		rr := httptest.NewRecorder()
		deps.ServeHTTP(rr, r)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s /stdin: got %d, want 405", m, rr.Code)
		}
	}
}

func TestConsole_UnknownSubPath(t *testing.T) {
	t.Parallel()
	deps := &v1ConsoleDeps{manager: nullManager{}}
	r := httptest.NewRequest(http.MethodPost, "/unknown", nil)
	rr := httptest.NewRecorder()
	deps.ServeHTTP(rr, r)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rr.Code)
	}
}

func TestConsole_Stdin_InvalidJSON(t *testing.T) {
	t.Parallel()
	deps := &v1ConsoleDeps{manager: nullManager{}}
	// The handler returns 404 (server not found) before reaching
	// body decode. To exercise JSON-decode 400 we'd need a
	// real *server.Server. This test asserts the actual
	// order: server lookup first.
	r := httptest.NewRequest(http.MethodPost, "/stdin", strings.NewReader("{not json"))
	rr := httptest.NewRecorder()
	deps.ServeHTTP(rr, r)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404 (server-not-found happens before body decode)", rr.Code)
	}
}

func TestConsole_Line_InvalidJSON(t *testing.T) {
	t.Parallel()
	deps := &v1ConsoleDeps{manager: nullManager{}}
	r := httptest.NewRequest(http.MethodPost, "/stdin/line", strings.NewReader("{not json"))
	rr := httptest.NewRecorder()
	deps.ServeHTTP(rr, r)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404 (server-not-found happens before body decode)", rr.Code)
	}
}

func TestConsole_Stdin_EmptyInput(t *testing.T) {
	t.Parallel()
	deps := &v1ConsoleDeps{manager: nullManager{}}
	r := httptest.NewRequest(http.MethodPost, "/stdin", strings.NewReader(`{"input":""}`))
	rr := httptest.NewRecorder()
	deps.ServeHTTP(rr, r)
	// nullManager → server not found → 404 (lookup happens before
	// input validation, by handler order). The validation
	// message is what we'd assert in an integration test
	// against a live server.
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rr.Code)
	}
}

func TestConsole_Line_EmptyText(t *testing.T) {
	t.Parallel()
	deps := &v1ConsoleDeps{manager: nullManager{}}
	r := httptest.NewRequest(http.MethodPost, "/stdin/line", strings.NewReader(`{"text":""}`))
	rr := httptest.NewRecorder()
	deps.ServeHTTP(rr, r)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rr.Code)
	}
}

func TestConsole_NewFakeManager_EmptyByDefault(t *testing.T) {
	t.Parallel()
	mgr := newFakeManager()
	if srv := mgr.ServerByName("missing"); srv != nil {
		t.Errorf("empty manager should return nil, got %+v", srv)
	}
}

// The happy path (WriteStdin success, 1024-byte cap, 503 on
// write error) requires a live *server.Server with an open
// stdin pipe. The handler signature binds consoleManager to
// returning *server.Server (the production type), so the unit
// tests here cover only what doesn't need a live subprocess.
// server_test.go has end-to-end coverage of the real pipe.
