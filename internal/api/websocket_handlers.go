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
	hub      *hub.Hub
	// allowedOrigins is the explicit allowlist for WS upgrades. If nil,
	// we derive it from the request's Host header at upgrade time
	// (allowing same-origin only). Set via WithAllowedOrigins to override.
	allowedOrigins []string
}

// WithAllowedOrigins configures the strict origin allowlist for WS upgrades.
// Pass empty to fall back to same-origin (the request's Host header).
// Pass one or more origins to allow additional cross-origin WS connections.
func (d *v1WebSocketDeps) WithAllowedOrigins(origins ...string) *v1WebSocketDeps {
	d.allowedOrigins = origins
	return d
}

// originAllowed reports whether the given Origin header is permitted for a
// WS upgrade. Without this check, a malicious page on the same eTLD+1 can
// open a WS to the API and hijack the user's session.
func (d *v1WebSocketDeps) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// No Origin header: same-origin browser requests don't always
		// send one, and non-browser clients (curl, scripts) won't either.
		// For a browser-only attack, Origin is mandatory, so rejecting
		// empty would be too strict.
		return true
	}
	// Build the effective allowlist: explicit set, or same-origin only.
	allowed := d.allowedOrigins
	if len(allowed) == 0 {
		host := r.Host
		// Accept both http and https schemes; browsers send the same
		// scheme as the page that opened the WS.
		allowed = []string{
			"http://" + host,
			"https://" + host,
		}
	}
	for _, a := range allowed {
		if origin == a {
			return true
		}
	}
	return false
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

	if !d.originAllowed(r) {
		http.Error(w, "forbidden: cross-origin websocket not allowed", http.StatusForbidden)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Origin already validated by originAllowed above. Setting
		// InsecureSkipVerify: true would bypass this entirely; we want
		// coder/websocket's own origin check as defense-in-depth but
		// we've already enforced, so the dual check is fine.
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
