package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Skomesh/MeshedServerTool/internal/motd"
	"github.com/Skomesh/MeshedServerTool/internal/storage"
)

func newTestMotdEnv(t *testing.T) *v1MotdDeps {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "motd-test.db") +
		"?mode=memory&cache=shared&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	store, err := storage.OpenFromDSN(dsn)
	if err != nil {
		t.Fatalf("OpenFromDSN: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &v1MotdDeps{store: motd.New(store)}
}

func (d *v1MotdDeps) callMotd(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reqBody *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reqBody = bytes.NewReader(raw)
	} else {
		reqBody = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, reqBody)
	rr := httptest.NewRecorder()
	d.ServeHTTP(rr, r)
	return rr
}

func TestMotd_List_Empty(t *testing.T) {
	t.Parallel()
	d := newTestMotdEnv(t)
	rr := d.callMotd(t, http.MethodGet, "/", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var resp struct {
		Global  *motd.MOTD   `json:"global"`
		Servers []*motd.MOTD `json:"servers"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Global == nil || resp.Global.Message != "" || resp.Global.Enabled {
		t.Errorf("expected empty/disabled global placeholder, got %+v", resp.Global)
	}
	if resp.Servers == nil || len(resp.Servers) != 0 {
		t.Errorf("expected empty servers array, got %v", resp.Servers)
	}
}

func TestMotd_List_AfterSets(t *testing.T) {
	t.Parallel()
	d := newTestMotdEnv(t)
	// Set global.
	if rr := d.callMotd(t, http.MethodPut, "/", map[string]string{"message": "global msg"}); rr.Code != http.StatusOK {
		t.Fatalf("set global: %d", rr.Code)
	}
	// Set two per-server entries.
	for _, name := range []string{"alpha", "beta"} {
		if rr := d.callMotd(t, http.MethodPut, "/"+name, map[string]string{"message": "msg-" + name}); rr.Code != http.StatusOK {
			t.Fatalf("set %s: %d", name, rr.Code)
		}
	}
	rr := d.callMotd(t, http.MethodGet, "/", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: %d", rr.Code)
	}
	var resp struct {
		Global  *motd.MOTD   `json:"global"`
		Servers []*motd.MOTD `json:"servers"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if resp.Global == nil || resp.Global.Message != "global msg" {
		t.Errorf("global not surfaced: %+v", resp.Global)
	}
	if len(resp.Servers) != 2 {
		t.Errorf("expected 2 server entries, got %d", len(resp.Servers))
	}
}

func TestMotd_GetForServer_FallsBackToGlobal(t *testing.T) {
	t.Parallel()
	d := newTestMotdEnv(t)
	// Set global.
	if rr := d.callMotd(t, http.MethodPut, "/", map[string]string{"message": "global!"}); rr.Code != http.StatusOK {
		t.Fatalf("set global: %d", rr.Code)
	}
	// Query a server with no per-server row.
	rr := d.callMotd(t, http.MethodGet, "/alpha", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get: %d", rr.Code)
	}
	var resp struct {
		Message   *motd.MOTD `json:"message"`
		PerServer bool       `json:"per_server"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if resp.Message == nil || resp.Message.Message != "global!" {
		t.Errorf("expected global fallback, got %+v", resp.Message)
	}
	if resp.PerServer {
		t.Errorf("per_server should be false on fallback, got true")
	}
}

func TestMotd_GetForServer_PerServerOverridesGlobal(t *testing.T) {
	t.Parallel()
	d := newTestMotdEnv(t)
	if rr := d.callMotd(t, http.MethodPut, "/", map[string]string{"message": "global!"}); rr.Code != http.StatusOK {
		t.Fatalf("set global: %d", rr.Code)
	}
	if rr := d.callMotd(t, http.MethodPut, "/alpha", map[string]string{"message": "alpha!"}); rr.Code != http.StatusOK {
		t.Fatalf("set alpha: %d", rr.Code)
	}
	rr := d.callMotd(t, http.MethodGet, "/alpha", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get: %d", rr.Code)
	}
	var resp struct {
		Message   *motd.MOTD `json:"message"`
		PerServer bool       `json:"per_server"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if resp.Message == nil || resp.Message.Message != "alpha!" {
		t.Errorf("per-server should win, got %+v", resp.Message)
	}
	if !resp.PerServer {
		t.Errorf("per_server should be true on override, got false")
	}
}

func TestMotd_GetForServer_NoGlobalEmpty(t *testing.T) {
	t.Parallel()
	d := newTestMotdEnv(t)
	// No global set, no per-server set.
	rr := d.callMotd(t, http.MethodGet, "/alpha", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get: %d", rr.Code)
	}
	var resp struct {
		Message   *motd.MOTD `json:"message"`
		PerServer bool       `json:"per_server"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if resp.Message == nil || resp.Message.Message != "" {
		t.Errorf("expected empty message, got %+v", resp.Message)
	}
	if resp.PerServer {
		t.Errorf("per_server should be false, got true")
	}
}

func TestMotd_SetGlobal_InvalidJSON(t *testing.T) {
	t.Parallel()
	d := newTestMotdEnv(t)
	r := httptest.NewRequest(http.MethodPut, "/", strings.NewReader("{not json"))
	rr := httptest.NewRecorder()
	d.ServeHTTP(rr, r)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rr.Code)
	}
}

func TestMotd_SetForServer_EnabledFalseDisables(t *testing.T) {
	t.Parallel()
	d := newTestMotdEnv(t)
	enabled := false
	rr := d.callMotd(t, http.MethodPut, "/alpha", map[string]any{
		"message": "alpha msg",
		"enabled": enabled,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("set: %d", rr.Code)
	}
	// Now query — per_server should still be true (the row exists
	// with enabled=0), and the message should be the disabled one.
	rr = d.callMotd(t, http.MethodGet, "/alpha", nil)
	var resp struct {
		Message   *motd.MOTD `json:"message"`
		PerServer bool       `json:"per_server"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if !resp.PerServer {
		t.Errorf("per_server should be true even when enabled=false, got false")
	}
	if resp.Message == nil || resp.Message.Message != "alpha msg" {
		t.Errorf("message: got %+v, want alpha msg", resp.Message)
	}
}

func TestMotd_DeleteForServer_NotFound(t *testing.T) {
	t.Parallel()
	d := newTestMotdEnv(t)
	rr := d.callMotd(t, http.MethodDelete, "/ghost", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rr.Code)
	}
}

func TestMotd_DeleteForServer_HappyPath(t *testing.T) {
	t.Parallel()
	d := newTestMotdEnv(t)
	if rr := d.callMotd(t, http.MethodPut, "/alpha", map[string]string{"message": "x"}); rr.Code != http.StatusOK {
		t.Fatalf("set: %d", rr.Code)
	}
	rr := d.callMotd(t, http.MethodDelete, "/alpha", nil)
	if rr.Code != http.StatusOK {
		t.Errorf("delete: %d", rr.Code)
	}
	// After delete, the server should not have a per-server row.
	rr = d.callMotd(t, http.MethodGet, "/alpha", nil)
	var resp struct {
		PerServer bool `json:"per_server"`
	}
	_ = json.NewDecoder(rr.Body).Decode(&resp)
	if resp.PerServer {
		t.Errorf("per_server should be false after delete")
	}
}

func TestMotd_MethodNotAllowed(t *testing.T) {
	t.Parallel()
	d := newTestMotdEnv(t)
	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/"},
		{http.MethodDelete, "/"},
		{http.MethodPatch, "/"},
		{http.MethodPost, "/alpha"},
		{http.MethodPatch, "/alpha"},
	}
	for _, tc := range cases {
		rr := d.callMotd(t, tc.method, tc.path, nil)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: got %d, want 405", tc.method, tc.path, rr.Code)
		}
	}
}

func TestMotd_SetForServer_FillsUpdatedByFromContext(t *testing.T) {
	t.Parallel()
	d := newTestMotdEnv(t)
	body, _ := json.Marshal(map[string]string{"message": "x"})
	r := stubAdminRequest(http.MethodPut, "/alpha")
	r.Body = newBody(body)
	r.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	d.ServeHTTP(rr, r)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var got motd.MOTD
	_ = json.NewDecoder(rr.Body).Decode(&got)
	if got.UpdatedBy != "admin" {
		t.Errorf("UpdatedBy: got %q, want admin", got.UpdatedBy)
	}
}

// newBody is a tiny helper to set the body of a request built by
// stubAdminRequest (which itself uses NopCloser(nil)).
func newBody(b []byte) *readCloserWrapper {
	return &readCloserWrapper{Reader: bytes.NewReader(b)}
}

type readCloserWrapper struct {
	*bytes.Reader
}

func (r *readCloserWrapper) Close() error { return nil }
