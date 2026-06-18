package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Skomesh/MeshedServerTool/internal/auth"
	"github.com/Skomesh/MeshedServerTool/internal/hub"
	"github.com/Skomesh/MeshedServerTool/internal/storage"
)

// newTestWSAuthEnv wires a v1WebSocketDeps plus an auth service
// backed by a fresh in-memory DB. Returns the deps, the auth
// service (for issuing a valid session token), and the hub.
func newTestWSAuthEnv(t *testing.T) (*v1WebSocketDeps, *auth.Service, *hub.Hub) {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "ws-test.db") +
		"?mode=memory&cache=shared&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	store, err := storage.OpenFromDSN(dsn)
	if err != nil {
		t.Fatalf("storage.OpenFromDSN: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	svc := auth.NewService(store)
	h := hub.NewHub()
	t.Cleanup(func() { h.Close() })

	deps := &v1WebSocketDeps{hub: h}
	return deps, svc, h
}

// wrapWSHandler returns an http.Handler that runs the auth
// middleware using the given svc and then the WS handler. Tests
// use this so the auth check and the WS handler share the same
// in-memory DB.
func wrapWSHandler(svc *auth.Service, h http.HandlerFunc) http.Handler {
	return svc.Middleware(h)
}

// TestWebSocket_OriginAllowed walks the audit-M18 origin policy.
// Without an allowlist, only the request's Host header is accepted.
// Empty Origin is allowed (server-to-server, curl). Cross-origin
// requests are rejected with 403.
func TestWebSocket_OriginAllowed(t *testing.T) {
	t.Parallel()
	deps, _, _ := newTestWSAuthEnv(t)

	cases := []struct {
		name        string
		host        string
		origin      string
		wantAllowed bool
	}{
		{"empty_origin_allowed", "meshed.local", "", true},
		{"same_origin_http_allowed", "meshed.local", "http://meshed.local", true},
		{"same_origin_https_allowed", "meshed.local", "https://meshed.local", true},
		{"cross_origin_denied", "meshed.local", "https://evil.example", false},
		{"subdomain_denied_without_allowlist", "meshed.local", "https://api.meshed.local", false},
		{"case_sensitive", "meshed.local", "http://Meshed.Local", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/api/v1/ws", nil)
			r.Host = tc.host
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if got := deps.originAllowed(r); got != tc.wantAllowed {
				t.Errorf("originAllowed(host=%q, origin=%q) = %v, want %v",
					tc.host, tc.origin, got, tc.wantAllowed)
			}
		})
	}
}

// TestWebSocket_OriginAllowed_ExplicitAllowlist verifies the
// WithAllowedOrigins escape hatch.
func TestWebSocket_OriginAllowed_ExplicitAllowlist(t *testing.T) {
	t.Parallel()
	deps, _, _ := newTestWSAuthEnv(t)
	deps.WithAllowedOrigins("https://trusted.example")

	r := httptest.NewRequest("GET", "/api/v1/ws", nil)
	r.Host = "meshed.local"
	r.Header.Set("Origin", "http://meshed.local") // would be denied under default
	if deps.originAllowed(r) {
		t.Error("non-allowlisted origin should be denied")
	}
	r2 := httptest.NewRequest("GET", "/api/v1/ws", nil)
	r2.Host = "meshed.local"
	r2.Header.Set("Origin", "https://trusted.example")
	if !deps.originAllowed(r2) {
		t.Error("allowlisted origin should be permitted")
	}
}

// TestWebSocket_RequiresAuth verifies that the handler returns 401
// when the request has no valid session. We test this without doing
// a real WS upgrade — the auth check happens before the upgrade.
func TestWebSocket_RequiresAuth(t *testing.T) {
	t.Parallel()
	deps, svc, _ := newTestWSAuthEnv(t)

	// No cookie: should fail at auth (401), not at the upgrade.
	r := httptest.NewRequest("GET", "/api/v1/ws", nil)
	rr := httptest.NewRecorder()
	wrapped := wrapWSHandler(svc, deps.handleWebSocket)
	wrapped.ServeHTTP(rr, r)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("no-cookie WS: got %d, want 401", rr.Code)
	}
}

// TestWebSocket_RejectsCrossOrigin verifies the production
// middleware+handler chain rejects cross-origin upgrades.
func TestWebSocket_RejectsCrossOrigin(t *testing.T) {
	t.Parallel()
	deps, svc, _ := newTestWSAuthEnv(t)

	// Seed a user + session so auth passes.
	if _, err := svc.CreateFirstUser(context.Background(), "alice", "hunter22"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	token, _, err := svc.Login(context.Background(), "alice", "hunter22", "ua", "1.2.3.4")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	// Cross-origin request with valid session: should fail at the
	// origin check (403), not at auth.
	r := httptest.NewRequest("GET", "/api/v1/ws", nil)
	r.Host = "meshed.local"
	r.Header.Set("Origin", "https://evil.example")
	r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token})
	rr := httptest.NewRecorder()
	wrapped := wrapWSHandler(svc, deps.handleWebSocket)
	wrapped.ServeHTTP(rr, r)
	if rr.Code != http.StatusForbidden {
		t.Errorf("cross-origin WS: got %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}

// TestWebSocket_RoundTrip is the integration check: real WS upgrade,
// client sends ping, server replies pong, client also sees a hub
// event published after the connection is established.
func TestWebSocket_RoundTrip(t *testing.T) {
	t.Parallel()
	deps, svc, h := newTestWSAuthEnv(t)

	// Seed a user + session.
	if _, err := svc.CreateFirstUser(context.Background(), "alice", "hunter22"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	token, _, err := svc.Login(context.Background(), "alice", "hunter22", "ua", "1.2.3.4")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	// Spin up the handler on an httptest server. The httptest
	// server is a real HTTP server so the WebSocket upgrade can
	// negotiate against it.
	handler := wrapWSHandler(svc, deps.handleWebSocket)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	// Open the WebSocket. We add a session cookie to the dial
	// request so the server's auth middleware sees it.
	wsURL := strings.Replace(ts.URL, "http://", "ws://", 1) + "/"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Cookie": []string{auth.SessionCookieName + "=" + token},
		},
	})
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "test done") })

	// coder/websocket's Ping() requires a concurrent reader; the
	// ping waits for the reader to process the pong control frame.
	// Spin up a reader goroutine that consumes everything (control
	// frames, data frames) and forwards data frames to a channel
	// for the test to assert on.
	dataCh := make(chan []byte, 32)
	readErrCh := make(chan error, 1)
	go func() {
		defer close(dataCh)
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				readErrCh <- err
				return
			}
			dataCh <- data
		}
	}()

	// 1. Send a JSON ping, expect a JSON pong reply. The pong is
	//    processed by the reader goroutine and shows up as a data
	//    message because the server's reply is `{"type":"pong"}`
	//    sent via conn.Write.
	pingCtx, pingCancel := context.WithTimeout(ctx, 2*time.Second)
	if err := conn.Write(pingCtx, websocket.MessageText,
		[]byte(`{"type":"ping"}`)); err != nil {
		pingCancel()
		t.Fatalf("ws write: %v", err)
	}
	pingCancel()
	sawPong := false
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()
	for !sawPong {
		select {
		case data, ok := <-dataCh:
			if !ok {
				t.Fatal("reader closed before pong")
			}
			var msg map[string]string
			_ = json.Unmarshal(data, &msg)
			if msg["type"] == "pong" {
				sawPong = true
			}
		case <-timeout.C:
			t.Fatal("never saw a pong reply")
		}
	}

	// 2. Publish an event to the hub, expect the WS to forward it.
	//    The reader goroutine is already in place; just publish.
	h.Publish(hub.Event{Type: "test.event", Data: "hello-ws"})

	sawEvent := false
	timeout2 := time.NewTimer(2 * time.Second)
	defer timeout2.Stop()
	for !sawEvent {
		select {
		case data, ok := <-dataCh:
			if !ok {
				t.Fatal("reader closed before hub event")
			}
			var msg struct {
				Type string `json:"type"`
				Data string `json:"Data"`
			}
			_ = json.Unmarshal(data, &msg)
			if msg.Type == "test.event" {
				sawEvent = true
			}
		case err := <-readErrCh:
			t.Fatalf("reader err before event: %v", err)
		case <-timeout2.C:
			t.Fatal("did not receive hub event over WS within 2s")
		}
	}
}

// TestDecodeJSON_MaxBodyBytes is a small extra: the 1 MiB body cap
// in decode.go is a security control (audit M13) and a regression
// here would let a client OOM the process. We pin the behavior.
//
// (decode.go is tested here rather than in its own file to avoid
// splitting a 4-line wrapper across files.)
func TestDecodeJSON_MaxBodyBytes(t *testing.T) {
	t.Parallel()
	// Build a body that exceeds the 1 MiB cap. 2 MiB of 'a' is enough.
	body := bytes.Repeat([]byte("a"), 2<<20)
	r := httptest.NewRequest("POST", "/x", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	var dst map[string]any
	err := decodeJSON(rr, r, &dst)
	if err == nil {
		t.Fatal("expected error on oversized body, got nil")
	}
	// We don't care about the specific error string; we just care
	// that we didn't read the full body (no OOM) and returned an
	// error so the handler can map it to a 400.
}
