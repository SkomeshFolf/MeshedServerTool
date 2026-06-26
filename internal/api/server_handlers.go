package api

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Skomesh/MeshedServerTool/internal/server"
	"github.com/Skomesh/MeshedServerTool/internal/storage"
)

// v1ServerDeps groups server-API dependencies. Implements http.Handler
// so it can be passed through http.StripPrefix.
type v1ServerDeps struct {
	store                    *storage.Store
	manager                  *server.Manager
	installRoot              string   // primary allowed base for install_dir
	extraInstallRoots        []string // additional allowed bases (OR'd with installRoot)
	allowedBinRoots          []string // extra path prefixes that executable may live under (e.g. /opt/scpsl)
	allowArbitraryExe        bool     // CRIT-1 escape hatch — disables executable allowlist
	allowArbitraryInstallDir bool     // CRIT-2 escape hatch — disables install_dir under-root check
}

// SetInstallRoot configures the validation bases for install_dir and
// the executable allowlist. Must be called before the router is
// exposed (in NewRouter).
//
//   - root: primary allowed base. install_dir must live under it.
//   - extra: additional allowed bases (OR'd with root). Use to whitelist
//     Steam install dirs, /opt/scpsl, etc. without going full-arbitrary.
//   - allowedBinRoots: extra absolute path prefixes that executable may
//     live under (in addition to system bins).
//   - allowArbitraryExe: disables the executable allowlist (CRIT-1 escape).
//   - allowArbitraryInstallDir: disables the install_dir under-root check
//     entirely (CRIT-2 escape). Use only in dev/testing or behind an
//     external sandbox.
func (d *v1ServerDeps) SetInstallRoot(
	root string,
	extra []string,
	allowedBinRoots []string,
	allowArbitraryExe bool,
	allowArbitraryInstallDir bool,
) {
	d.installRoot = filepath.Clean(root)
	d.extraInstallRoots = extra
	d.allowedBinRoots = allowedBinRoots
	d.allowArbitraryExe = allowArbitraryExe
	d.allowArbitraryInstallDir = allowArbitraryInstallDir
}

// validateInstallDir returns nil if the path is acceptable, or an error
// describing why it was rejected. Used at create/update time. (CRIT-2)
//
// Rules:
//   - must be absolute
//   - must not contain '..' after filepath.Clean
//   - if d.allowArbitraryInstallDir is true, no under-root check runs
//   - otherwise: must live under d.installRoot OR any of d.extraInstallRoots
//   - must not be one of the reserved system paths
func (d *v1ServerDeps) validateInstallDir(p string) error {
	if p == "" {
		return errors.New("install_dir is required")
	}
	if !filepath.IsAbs(p) {
		return errors.New("install_dir must be an absolute path")
	}
	cleaned := filepath.Clean(p)
	if strings.Contains(cleaned, "..") {
		return errors.New("install_dir must not contain '..'")
	}
	// Allow-arbitrary escape: skip the under-root check entirely.
	// The operator has opted into trusting this. Reserved path checks
	// still apply below as a defense-in-depth backstop.
	if !d.allowArbitraryInstallDir {
		if d.installRoot == "" && len(d.extraInstallRoots) == 0 {
			return errors.New("server validation not configured (install_root missing)")
		}
		allowed := false
		if d.installRoot != "" {
			root := filepath.Clean(d.installRoot)
			if rel, err := filepath.Rel(root, cleaned); err == nil && rel != ".." && !strings.HasPrefix(rel, "..") {
				allowed = true
			}
		}
		if !allowed {
			for _, extra := range d.extraInstallRoots {
				extraClean := filepath.Clean(extra)
				if rel, err := filepath.Rel(extraClean, cleaned); err == nil && rel != ".." && !strings.HasPrefix(rel, "..") {
					allowed = true
					break
				}
			}
		}
		if !allowed {
			roots := []string{d.installRoot}
			roots = append(roots, d.extraInstallRoots...)
			return fmt.Errorf("install_dir must be under one of: %s", strings.Join(roots, ", "))
		}
	}
	// Reserved system paths (defense in depth — the install_root check
	// already prevents these in most cases, but if someone sets
	// --install-root=/ we want an extra signal).
	for _, reserved := range []string{
		"/etc", "/proc", "/sys", "/dev", "/boot", "/bin", "/sbin",
		"/usr/bin", "/usr/sbin", "/usr/lib", "/usr/include",
		"/var/lib/dpkg", "/var/lib/rpm",
	} {
		if cleaned == reserved || strings.HasPrefix(cleaned, reserved+"/") {
			return fmt.Errorf("install_dir under reserved system path: %s", reserved)
		}
	}
	return nil
}

// validateExecutablePath returns nil if executable is acceptable, or an error.
// Used at create/update time. (CRIT-1)
//
// Rules (in order):
//   - empty is OK (server has no command yet; user configures later)
//   - if d.allowArbitraryExe is true, anything goes (tests only)
//   - must be absolute
//   - must live under install_dir, OR under one of d.extraInstallRoots,
//     OR under one of d.allowedBinRoots, OR under the system bin set
//   - must not contain '..'
func (d *v1ServerDeps) validateExecutablePath(exe, installDir string) error {
	if exe == "" {
		return nil
	}
	if d.allowArbitraryExe {
		return nil
	}
	if !filepath.IsAbs(exe) {
		return errors.New("executable must be an absolute path")
	}
	cleaned := filepath.Clean(exe)
	if strings.Contains(cleaned, "..") {
		return errors.New("executable must not contain '..'")
	}
	// Under install_dir is always fine.
	if installDir != "" {
		instClean := filepath.Clean(installDir)
		if rel, err := filepath.Rel(instClean, cleaned); err == nil && rel != ".." && !strings.HasPrefix(rel, "..") {
			return nil
		}
	}
	// Under any extra install root is also fine — if you whitelisted
	// /opt/scpsl as an install dir, executables under there should
	// be allowed without an extra --allowed-bin-roots flag.
	for _, extra := range d.extraInstallRoots {
		prefix := filepath.Clean(extra)
		if cleaned == prefix || strings.HasPrefix(cleaned, prefix+"/") {
			return nil
		}
	}
	// Then check system path allowlist (defaults plus user-configured).
	allowed := append([]string{"/bin", "/sbin", "/usr/bin", "/usr/sbin", "/usr/local/bin"}, d.allowedBinRoots...)
	for _, prefix := range allowed {
		prefix = filepath.Clean(prefix)
		if cleaned == prefix || strings.HasPrefix(cleaned, prefix+"/") {
			return nil
		}
	}
	return fmt.Errorf("executable must be under install_dir, a system bin path, or use --allow-arbitrary-executable")
}

// execArgs extracts and validates the args.executable and args.argv from
// a request body. Called by createServer and updateServer. (CRIT-1)
func (d *v1ServerDeps) execArgsFromBody(args map[string]any, installDir string) (string, []string, error) {
	if args == nil {
		return "", nil, nil
	}
	exe, _ := args["executable"].(string)
	if err := d.validateExecutablePath(exe, installDir); err != nil {
		return "", nil, err
	}
	var argv []string
	if raw, ok := args["argv"].([]any); ok {
		for _, a := range raw {
			s, ok := a.(string)
			if !ok {
				continue // silently drop non-strings (matches buildCommand behavior)
			}
			argv = append(argv, s)
		}
	}
	return exe, argv, nil
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
	if err := d.validateInstallDir(body.InstallDir); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, _, err := d.execArgsFromBody(body.Args, body.InstallDir); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
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
		InstallDir *string        `json:"install_dir"`
		Port       *int           `json:"port"`
		MaxPlayers *int           `json:"max_players"`
		Hostname   *string        `json:"hostname"`
		Args       map[string]any `json:"args"`
		Autostart  *bool          `json:"autostart"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.InstallDir != nil {
		if err := d.validateInstallDir(*body.InstallDir); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		existing.InstallDir = *body.InstallDir
	}
	if body.Args != nil {
		if _, _, err := d.execArgsFromBody(body.Args, existing.InstallDir); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		existing.Args = body.Args
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
//	GET ?tail=200   → JSON {lines: [...]} (most recent N, default 200, max 5000)
//
// Real-time log streaming is delivered over WebSocket (see
// internal/api/websocket_handlers.go); the React useWebSocket hook is the
// single subscription path on the frontend.
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

	tailStr := r.URL.Query().Get("tail")
	if tailStr == "" {
		tailStr = "200"
	}
	n, err := strconv.Atoi(tailStr)
	if err != nil || n < 0 {
		writeJSONError(w, http.StatusBadRequest, "tail must be a non-negative integer")
		return
	}
	if n > 5000 {
		n = 5000
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": buf.Snapshot(n)})
}
