package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/Skomesh/MeshedServerTool/internal/auth"
	"github.com/Skomesh/MeshedServerTool/internal/hub"
)

// v1WebSocketDeps groups the WebSocket endpoint's dependencies.
type v1WebSocketDeps struct {
	hub *hub.Hub
}

// handleWebSocket upgrades the request to a WebSocket and streams hub
// events to the client. Authentication uses the same session cookie as
// the REST API; the upgrade only proceeds for valid sessions.
//
// Wire format: each message is a JSON-encoded hub.Event, so the client
// sees the same `{type, data}` shape we publish from the manager and
// log buffers. The client may also send JSON commands — currently the
// only one recognized is `{"type": "ping"}` which the server replies to
// with `{"type": "pong"}` for keepalive.
func (d *v1WebSocketDeps) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Auth check: the session middleware sets the user on the context.
	// We require it for /api/v1/ws (no anonymous WS).
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Permissive origin check: this is a single-user local app and
		// the cookie-based auth doesn't have CSRF exposure anyway. A
		// future hardening pass should add a strict origin allowlist.
		InsecureSkipVerify: true,
	})
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		return
	}
	defer conn.Close(websocket.StatusInternalError, "shutting down")

	sub := d.hub.Subscribe()
	defer d.hub.Unsubscribe(sub)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Reader goroutine: process inbound messages (keepalive pings, etc).
	// We don't expect many but the spec requires we respond to control
	// frames; coder/websocket does pongs automatically.
	go func() {
		defer cancel()
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var msg struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			if msg.Type == "ping" {
				_ = conn.Write(ctx, websocket.MessageText,
					[]byte(`{"type":"pong"}`))
			}
		}
	}()

	// Writer loop: forward hub events to the client, plus a keepalive
	// ping every 30s so idle connections don't get reaped.
	keepalive := time.NewTicker(30 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-sub.C:
			if !ok {
				return
			}
			payload, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
				return
			}
		case <-keepalive.C:
			// Server-initiated ping; coder/websocket handles pong
			// replies automatically.
			if err := conn.Ping(ctx); err != nil {
				return
			}
		}
	}
}
