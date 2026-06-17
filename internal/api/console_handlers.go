package api

import (
	"log"
	"net/http"
	"strings"

	"github.com/Skomesh/MeshedServerTool/internal/server"
)

// v1ConsoleDeps wires the per-server stdin/console API.
//
//	POST /api/v1/servers/{name}/stdin       — write raw bytes (caller adds \n)
//	POST /api/v1/servers/{name}/stdin/line  — write a line, \n appended for you
//
// Both endpoints require the server to be running and have a live
// stdin pipe. Auth is applied at the router (authSvc.Middleware);
// this handler assumes the caller is already authenticated.
type v1ConsoleDeps struct {
	manager consoleManager
}

// consoleManager is the subset of *server.Manager used here. Defined
// as an interface so tests can fake the manager without spinning up
// a real subprocess.
type consoleManager interface {
	ServerByName(name string) *server.Server
}

type consoleServer interface {
	WriteStdin(p []byte) error
}

func (d *v1ConsoleDeps) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	parts := strings.SplitN(path, "/", 2)
	serverName := parts[0]
	rest := ""
	if len(parts) > 1 {
		rest = parts[1]
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	srv := d.manager.ServerByName(serverName)
	if srv == nil {
		writeJSONError(w, http.StatusNotFound, "server not found")
		return
	}

	switch rest {
	case "stdin/line":
		d.handleLine(w, r, srv)
	case "stdin":
		d.handleRaw(w, r, srv)
	default:
		http.Error(w, "not found: "+r.URL.Path, http.StatusNotFound)
	}
}

// handleRaw writes the caller-supplied bytes verbatim. The body must
// be valid JSON of the form {"input": "..."} — newline framing is the
// caller's responsibility, so an admin can send partial lines if they
// need to (e.g. a Ctrl-D / EOF for game consoles that need it).
func (d *v1ConsoleDeps) handleRaw(w http.ResponseWriter, r *http.Request, srv consoleServer) {
	var body struct {
		Input string `json:"input"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Input == "" {
		writeJSONError(w, http.StatusBadRequest, "input is required")
		return
	}
	if len(body.Input) > 1024 {
		writeJSONError(w, http.StatusBadRequest, "input exceeds 1024 bytes")
		return
	}
	if err := srv.WriteStdin([]byte(body.Input)); err != nil {
		// The only error path is "server is not running" (the
		// process exited between the manager check and the write)
		// or a write error (process died, pipe broken). Both are
		// service-availability problems from the client's POV.
		log.Printf("stdin write: %v", err)
		writeJSONError(w, http.StatusServiceUnavailable, "stdin not available: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleLine is the convenience endpoint the React UI uses. Body is
// {"text": "kick PlayerName"} (no trailing newline); the handler
// appends "\n" before writing.
func (d *v1ConsoleDeps) handleLine(w http.ResponseWriter, r *http.Request, srv consoleServer) {
	var body struct {
		Text string `json:"text"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Text == "" {
		writeJSONError(w, http.StatusBadRequest, "text is required")
		return
	}
	// Cap at 1023 bytes of text + 1 byte for the newline = 1024 total.
	if len(body.Text) > 1023 {
		writeJSONError(w, http.StatusBadRequest, "text exceeds 1023 bytes")
		return
	}
	payload := body.Text + "\n"
	if err := srv.WriteStdin([]byte(payload)); err != nil {
		log.Printf("stdin write: %v", err)
		writeJSONError(w, http.StatusServiceUnavailable, "stdin not available: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
