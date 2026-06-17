package api

import (
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Skomesh/MeshedServerTool/internal/server"
	"github.com/Skomesh/MeshedServerTool/internal/settings"
	"github.com/Skomesh/MeshedServerTool/internal/storage"
)

// v1TabsDeps wires the per-server settings-tab API.
//
// The v2 frontend exposed five per-server tabs (Map, Players, Server,
// Gameplay, Management) backed by INI files in the server's install dir.
// In v3 the management fields (name/install_dir/port/max_players/…)
// already live on the `servers` row and are editable through the
// existing detail-page form, so the four INI-driven tabs are all we need
// to port.
//
// Routes (all auth-protected, mounted under /api/v1/servers/{name}/tabs/):
//
//	GET /api/v1/servers/{name}/tabs/map        → {entries: {key: value}}
//	PUT /api/v1/servers/{name}/tabs/map        body {key: value, ...}
//	GET /api/v1/servers/{name}/tabs/players    → {admins, owners, whitelist: []string}
//	PUT /api/v1/servers/{name}/tabs/players    body {admins, owners, whitelist: []string}
//	GET /api/v1/servers/{name}/tabs/server     → {entries: {key: value}} (excl. GameplayConfig)
//	PUT /api/v1/servers/{name}/tabs/server     body {key: value, ...}
//	GET /api/v1/servers/{name}/tabs/gameplay   → {entries: {key: value}}
//	PUT /api/v1/servers/{name}/tabs/gameplay   body {key: value, ...}
type v1TabsDeps struct {
	store   *storage.Store
	manager *server.Manager
}

// sectionGameInstance is the UE section name that holds both the
// non-GameplayConfig "Server" entries and the GameplayConfig blob.
const sectionGameInstance = "/Game/SCPPandemic/Blueprints/GI_PandemicGameInstance.GI_PandemicGameInstance_C"

// sectionMap is the section in ServerConfig.ini used by the Map tab.
const sectionMap = "Map"

// GameplayConfigKey is the entry key that holds the gameplay k=v blob.
const GameplayConfigKey = "GameplayConfig"

func (d *v1TabsDeps) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// /api/v1/servers/{name}/tabs/{tab} — the router strips
	// /api/v1/servers and sets X-Meshed-Server-Name, so r.URL.Path is
	// now "{name}/tabs/{tab}" or "{name}/tabs/{tab}/". Take the part
	// after "/tabs/".
	trimmed := strings.TrimPrefix(r.URL.Path, "/")
	trimmed = strings.TrimSuffix(trimmed, "/")
	idx := strings.Index(trimmed, "/tabs/")
	var tab string
	if idx >= 0 {
		tab = trimmed[idx+len("/tabs/"):]
	} else if strings.HasSuffix(trimmed, "/tabs") {
		tab = ""
	} else {
		writeJSONError(w, http.StatusNotFound, "not a tabs path: "+r.URL.Path)
		return
	}
	if tab == "" {
		writeJSONError(w, http.StatusNotFound, "tab name required")
		return
	}

	name, err := tabServerName(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !nameRE.MatchString(name) {
		writeJSONError(w, http.StatusBadRequest, "invalid server name")
		return
	}

	installDir, err := d.installDirFor(r, name)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "server not found")
			return
		}
		log.Printf("tabs: install_dir lookup: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if _, err := os.Stat(installDir); err != nil {
		if os.IsNotExist(err) {
			writeJSONError(w, http.StatusNotFound, "install_dir does not exist: "+installDir)
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "stat install_dir failed")
		return
	}

	switch tab {
	case "map":
		d.serveMap(w, r, installDir)
	case "players":
		d.servePlayers(w, r, installDir)
	case "server":
		d.serveServer(w, r, installDir)
	case "gameplay":
		d.serveGameplay(w, r, installDir)
	default:
		writeJSONError(w, http.StatusNotFound, "unknown tab: "+tab)
	}
}

// tabServerName extracts the server name from a request URL like
// /api/v1/servers/{name}/tabs/{tab} — the dispatcher sets a header to
// carry it through StripPrefix; we read it back here.
func tabServerName(r *http.Request) (string, error) {
	if n := r.Header.Get("X-Meshed-Server-Name"); n != "" {
		return n, nil
	}
	// Fallback: reparse the original (pre-strip) path if available.
	full := r.Header.Get("X-Meshed-Full-Path")
	if full == "" {
		return "", errors.New("missing server name")
	}
	// full is "/api/v1/servers/{name}/tabs/{tab}" — split out the name.
	parts := strings.Split(full, "/")
	// expect ["", "api", "v1", "servers", name, "tabs", ...]
	if len(parts) < 6 || parts[3] != "servers" || parts[5] != "tabs" {
		return "", errors.New("unexpected path shape: " + full)
	}
	return parts[4], nil
}

func (d *v1TabsDeps) installDirFor(r *http.Request, serverName string) (string, error) {
	srv, err := d.store.Servers().GetServer(r.Context(), serverName)
	if err != nil {
		return "", err
	}
	return srv.InstallDir, nil
}

// ---- map tab ----

func (d *v1TabsDeps) serveMap(w http.ResponseWriter, r *http.Request, installDir string) {
	path := filepath.Join(installDir, "Config", "ServerConfig.ini")
	switch r.Method {
	case http.MethodGet:
		f, err := settings.Parse(path)
		if err != nil {
			log.Printf("map parse: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"entries": sectionToMap(f, sectionMap)})
	case http.MethodPut:
		var body map[string]string
		if err := decodeJSON(w, r, &body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		f, err := settings.Parse(path)
		if err != nil {
			log.Printf("map parse (put): %v", err)
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		f.Path = path
		// v2 quirk: rewrite `~` → `?` and `-` → `=` in values before saving.
		for k, v := range body {
			v = strings.ReplaceAll(v, "~", "?")
			v = strings.ReplaceAll(v, "-", "=")
			f.SetValue(sectionMap, k, v)
		}
		if err := f.Write(); err != nil {
			log.Printf("map write: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "entries": sectionToMap(f, sectionMap)})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ---- server tab ----

func (d *v1TabsDeps) serveServer(w http.ResponseWriter, r *http.Request, installDir string) {
	path := filepath.Join(installDir, "Config", "ServerConfig.ini")
	switch r.Method {
	case http.MethodGet:
		f, err := settings.Parse(path)
		if err != nil {
			log.Printf("server parse: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		entries := sectionToMap(f, sectionGameInstance)
		// v2 spec: exclude GameplayConfig from the Server tab — it's the
		// Gameplay tab's responsibility.
		delete(entries, GameplayConfigKey)
		writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
	case http.MethodPut:
		var body map[string]string
		if err := decodeJSON(w, r, &body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		f, err := settings.Parse(path)
		if err != nil {
			log.Printf("server parse (put): %v", err)
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		f.Path = path
		// Only touch non-GameplayConfig keys here; gameplay PUT owns that key.
		for k, v := range body {
			if k == GameplayConfigKey {
				continue
			}
			f.SetValue(sectionGameInstance, k, v)
		}
		if err := f.Write(); err != nil {
			log.Printf("server write: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ---- gameplay tab ----

var (
	gpStripParensRE = regexp.MustCompile(`^\(|\)$`)
)

// parseGameplayConfig parses the v2 GameplayConfig value blob
// "GameplayConfig=(k=v,k=v,...)" into a flat string map. Empty values
// yield an empty map.
func parseGameplayConfig(value string) map[string]string {
	out := map[string]string{}
	v := strings.TrimSpace(value)
	if v == "" {
		return out
	}
	// Strip a single leading '(' and a single trailing ')'. v2's blob
	// is wrapped exactly once but if a user wrote more parens we just
	// strip the outermost pair and treat the rest as part of the value.
	v = gpStripParensRE.ReplaceAllString(v, "")
	v = strings.TrimSpace(v)
	if v == "" {
		return out
	}
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		eq := strings.IndexByte(part, '=')
		if eq <= 0 {
			// malformed — skip rather than 500 the whole file
			continue
		}
		k := strings.TrimSpace(part[:eq])
		val := strings.TrimSpace(part[eq+1:])
		out[k] = val
	}
	return out
}

// serializeGameplayConfig turns a flat string map back into the v2
// shape: "(k=v,k=v,...)". Values are emitted exactly as stored — the
// caller (frontend) decides whether to send a bool, int, or float
// literal; we don't coerce here so that a `bOverrideDefaults=True` and
// `RecoilMultiplier=0.5` round-trip identically.
func serializeGameplayConfig(entries map[string]string) string {
	if len(entries) == 0 {
		return ""
	}
	// Stable order: sort keys. v2 used Python 3.7+ dict order which
	// happens to be insertion order — for round-trip stability we sort,
	// matching the practical v2 behaviour (the config blob is rarely
	// hand-edited).
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	// Stable alphabetical sort.
	sortStrings(keys)
	var b strings.Builder
	b.WriteByte('(')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(entries[k])
	}
	b.WriteByte(')')
	return b.String()
}

// sortStrings is a tiny inlined sort.Strings to avoid pulling in the
// "sort" package only for this. Strings are short.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

func (d *v1TabsDeps) serveGameplay(w http.ResponseWriter, r *http.Request, installDir string) {
	path := filepath.Join(installDir, "Config", "ServerConfig.ini")
	switch r.Method {
	case http.MethodGet:
		f, err := settings.Parse(path)
		if err != nil {
			log.Printf("gameplay parse: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		var blob string
		for _, s := range f.Sections {
			if s.Name != sectionGameInstance {
				continue
			}
			for _, e := range s.Entries {
				if e.Key == GameplayConfigKey {
					blob = e.Value
				}
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"entries": parseGameplayConfig(blob)})
	case http.MethodPut:
		var body map[string]string
		if err := decodeJSON(w, r, &body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		f, err := settings.Parse(path)
		if err != nil {
			log.Printf("gameplay parse (put): %v", err)
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		f.Path = path
		newBlob := serializeGameplayConfig(body)
		f.SetValue(sectionGameInstance, GameplayConfigKey, newBlob)
		if err := f.Write(); err != nil {
			log.Printf("gameplay write: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ---- players tab ----

// playersFile maps the JSON list name to the file in install_dir.
var playersFile = map[string]string{
	"admins":    "AdminIDs.ini",
	"owners":    "OwnerIDs.ini",
	"whitelist": "WhitelistIDs.ini",
}

func (d *v1TabsDeps) servePlayers(w http.ResponseWriter, r *http.Request, installDir string) {
	switch r.Method {
	case http.MethodGet:
		out := map[string][]string{}
		for list, name := range playersFile {
			ids, err := readSteamIDList(filepath.Join(installDir, name))
			if err != nil {
				log.Printf("players read %s: %v", name, err)
				writeJSONError(w, http.StatusInternalServerError, "internal error")
				return
			}
			out[list] = ids
		}
		writeJSON(w, http.StatusOK, out)
	case http.MethodPut:
		var body map[string][]string
		if err := decodeJSON(w, r, &body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		for list, name := range playersFile {
			ids := body[list]
			if ids == nil {
				// Frontend is allowed to omit a list to leave it untouched;
				// empty array means "clear the list".
				continue
			}
			if err := writeSteamIDList(filepath.Join(installDir, name), ids); err != nil {
				log.Printf("players write %s: %v", name, err)
				writeJSONError(w, http.StatusInternalServerError, "internal error")
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// readSteamIDList reads one Steam ID per line, ignoring blank lines and
// trailing whitespace. Missing files return an empty list and no error.
func readSteamIDList(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		id := strings.TrimSpace(line)
		if id == "" {
			continue
		}
		out = append(out, id)
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}

// writeSteamIDList writes one Steam ID per line. Trims and drops empty
// entries, atomic-ish (write to temp + rename within the install_dir).
func writeSteamIDList(path string, ids []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		b.WriteString(id)
		b.WriteByte('\n')
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".players-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(b.String()); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

// ---- shared helpers ----

// sectionToMap flattens one section's entries to a map. Missing section
// yields an empty (non-nil) map.
func sectionToMap(f *settings.File, name string) map[string]string {
	out := map[string]string{}
	if f == nil {
		return out
	}
	for _, s := range f.Sections {
		if s.Name != name {
			continue
		}
		for _, e := range s.Entries {
			out[e.Key] = e.Value
		}
	}
	return out
}

// formatBool returns the v2 GameplayConfig literal spelling.
func formatBool(b bool) string { return strconv.FormatBool(b) }

// keep fmt referenced if future edits remove the one usage above.
var _ = error(nil)
