package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Skomesh/MeshedServerTool/internal/auth"
	"github.com/Skomesh/MeshedServerTool/internal/server"
	"github.com/Skomesh/MeshedServerTool/internal/storage"
)

// v1ServerDeps groups server-API dependencies. Implements http.Handler
// so it can be passed through http.StripPrefix.
type v1ServerDeps struct {
	store   *storage.Store
	manager *server.Manager
}

// ServeHTTP routes /api/v1/servers/* requests.
//
//	GET    /api/v1/servers                  — list
//	POST   /api/v1/servers                  — create
//	GET    /api/v1/servers/{name}           — get one
//	PATCH  /api/v1/servers/{name}           — update config
//	DELETE /api/v1/servers/{name}           — delete
var nameRE = regexp.MustCompile(`^[a-zA-Z0-9_-]{3,64}$`)

func (d *v1ServerDeps) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// /api/v1/servers        →  ""
	// /api/v1/servers/       →  ""
	// /api/v1/servers/alpha  →  "alpha"
	// /api/v1/servers/alpha/start → "alpha/start"
	path := strings.TrimPrefix(r.URL.Path, "/")
	parts := strings.SplitN(path, "/", 2)
	name := parts[0]
	rest := ""
	if len(parts) > 1 {
		rest = parts[1]
	}

	if name == "" {
		switch r.Method {
		case http.MethodGet:
			d.listServers(w, r)
		case http.MethodPost:
			d.createServer(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	// Anything beyond the bare name is a sub-resource.
	if rest != "" {
		switch rest {
		case "start":
			d.lifecycleAction(w, r, name, "start")
		case "stop":
			d.lifecycleAction(w, r, name, "stop")
		case "restart":
			d.lifecycleAction(w, r, name, "restart")
		case "logs":
			d.handleLogs(w, r, name)
		default:
			http.Error(w, "not found: "+r.URL.Path, http.StatusNotFound)
		}
		return
	}

	switch r.Method {
	case http.MethodGet:
		d.getServer(w, r, name)
	case http.MethodPatch:
		d.updateServer(w, r, name)
	case http.MethodDelete:
		d.deleteServer(w, r, name)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (d *v1ServerDeps) listServers(w http.ResponseWriter, r *http.Request) {
	views := d.manager.List()
	if views == nil {
		views = []*storage.ServerView{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"servers": views})
}

func (d *v1ServerDeps) createServer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name       string         `json:"name"`
		InstallDir string         `json:"install_dir"`
		Port       int            `json:"port"`
		MaxPlayers int            `json:"max_players"`
		Hostname   string         `json:"hostname"`
		Args       map[string]any `json:"args"`
		Autostart  bool           `json:"autostart"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !nameRE.MatchString(body.Name) {
		writeJSONError(w, http.StatusBadRequest, "name must be 3-64 chars, [a-zA-Z0-9_-]")
		return
	}
	if body.InstallDir == "" {
		writeJSONError(w, http.StatusBadRequest, "install_dir is required")
		return
	}
	if body.Port == 0 {
		body.Port = 7777
	}
	if body.MaxPlayers == 0 {
		body.MaxPlayers = 32
	}
	if body.Args == nil {
		body.Args = map[string]any{}
	}

	srv := &storage.Server{
		Name: body.Name, InstallDir: body.InstallDir,
		Port: body.Port, MaxPlayers: body.MaxPlayers,
		Hostname: body.Hostname, Args: body.Args, Autostart: body.Autostart,
	}
	if err := d.store.Servers().CreateServer(r.Context(), srv); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			writeJSONError(w, http.StatusConflict, "a server with that name already exists")
			return
		}
		log.Printf("create server: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	d.manager.AddServer(srv)
	view := d.manager.Get(srv.Name)
	writeJSON(w, http.StatusCreated, view)
}

func (d *v1ServerDeps) getServer(w http.ResponseWriter, r *http.Request, name string) {
	view := d.manager.Get(name)
	if view == nil {
		writeJSONError(w, http.StatusNotFound, "server not found")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (d *v1ServerDeps) updateServer(w http.ResponseWriter, r *http.Request, name string) {
	existing := d.manager.Get(name)
	if existing == nil {
		writeJSONError(w, http.StatusNotFound, "server not found")
		return
	}
	var body struct {
		InstallDir *string         `json:"install_dir"`
		Port       *int            `json:"port"`
		MaxPlayers *int            `json:"max_players"`
		Hostname   *string         `json:"hostname"`
		Args       map[string]any  `json:"args"`
		Autostart  *bool           `json:"autostart"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.InstallDir != nil {
		existing.InstallDir = *body.InstallDir
	}
	if body.Port != nil {
		existing.Port = *body.Port
	}
	if body.MaxPlayers != nil {
		existing.MaxPlayers = *body.MaxPlayers
	}
	if body.Hostname != nil {
		existing.Hostname = *body.Hostname
	}
	if body.Args != nil {
		existing.Args = body.Args
	}
	if body.Autostart != nil {
		existing.Autostart = *body.Autostart
	}
	if err := d.store.Servers().UpdateServerConfig(r.Context(), &existing.Server); err != nil {
		log.Printf("update server: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	// Refetch the canonical config from the DB and push it into the
	// manager's in-memory cache. Without this, the manager's *Server.cfg
	// still points at the old struct, so the next Start() spawns the old
	// binary from the old install dir. (audit finding #1)
	updated, err := d.store.Servers().GetServer(r.Context(), name)
	if err != nil {
		log.Printf("refetch server: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := d.manager.UpdateConfig(name, updated); err != nil {
		log.Printf("manager update config: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, d.manager.Get(name))
}

func (d *v1ServerDeps) deleteServer(w http.ResponseWriter, r *http.Request, name string) {
	if d.manager.Get(name) == nil {
		writeJSONError(w, http.StatusNotFound, "server not found")
		return
	}
	// Delete from the DB first; only remove from the in-memory map on
	// success. The reverse order would leave the manager without a
	// server while the DB row still exists — a crash between the two
	// steps would re-add the server on next startup with stale state.
	// (audit finding #3)
	if err := d.store.Servers().DeleteServer(r.Context(), name); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "server not found")
			return
		}
		log.Printf("delete server: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := d.manager.RemoveServer(name); err != nil {
		log.Printf("remove from manager: %v", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d *v1ServerDeps) lifecycleAction(w http.ResponseWriter, r *http.Request, name, action string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if d.manager.Get(name) == nil {
		writeJSONError(w, http.StatusNotFound, "server not found")
		return
	}
	var err error
	switch action {
	case "start":
		err = d.manager.Start(r.Context(), name)
	case "stop":
		err = d.manager.Stop(r.Context(), name, 5*time.Second)
	case "restart":
		err = d.manager.Restart(r.Context(), name)
	}
	if err != nil {
		if errors.Is(err, server.ErrInvalidTransition) {
			writeJSONError(w, http.StatusConflict, err.Error())
			return
		}
		// Log the full error (which may include install paths and
		// binary names from fork/exec failures) but return a generic
		// message to the user. (audit finding #7)
		log.Printf("%s %s: %v", action, name, err)
		writeJSONError(w, http.StatusInternalServerError, "lifecycle action failed; see server logs")
		return
	}
	writeJSON(w, http.StatusOK, d.manager.Get(name))
}

// handleLogs serves /api/v1/servers/{name}/logs.
//
//   GET ?tail=200          → JSON {lines: [...]} (most recent N)
//   GET (no tail)          → SSE stream of new lines (Phase 3 will replace
//                            this with WebSocket; SSE is fine for now and
//                            works through every HTTP proxy).
func (d *v1ServerDeps) handleLogs(w http.ResponseWriter, r *http.Request, name string) {
	view := d.manager.Get(name)
	if view == nil {
		writeJSONError(w, http.StatusNotFound, "server not found")
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Find the buffer via the manager. We don't expose Server directly on
	// the manager; re-derive by listing and picking the matching one. The
	// in-memory Server carries the buffer.
	srv := lookupServer(d.manager, name)
	if srv == nil {
		writeJSONError(w, http.StatusNotFound, "server not found")
		return
	}
	buf := srv.LogBuffer()

	if tailStr := r.URL.Query().Get("tail"); tailStr != "" {
		n, err := strconv.Atoi(tailStr)
		if err != nil || n < 0 {
			writeJSONError(w, http.StatusBadRequest, "tail must be a non-negative integer")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"lines": buf.Snapshot(n)})
		return
	}

	// SSE stream of new lines. Auth must be re-checked here because the
	// middleware protects the parent route; SSE long-connections are fine
	// because Go's http.Server runs the handler in its own goroutine.
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable proxy buffering

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	sub := buf.Subscribe()
	defer buf.Unsubscribe(sub)

	// Send the last few lines on connect so the client doesn't have an
	// empty UI for the first few seconds.
	for _, line := range buf.Snapshot(50) {
		writeSSE(w, "line", line)
	}
	flusher.Flush()

	ctx := r.Context()
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-sub:
			if !ok {
				return
			}
			writeSSE(w, "line", line)
			flusher.Flush()
		case <-keepalive.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

func writeSSE(w http.ResponseWriter, event string, data any) {
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
}
