package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/Skomesh/MeshedServerTool/internal/bans"
	"github.com/Skomesh/MeshedServerTool/internal/server"
	"github.com/Skomesh/MeshedServerTool/internal/settings"
	"github.com/Skomesh/MeshedServerTool/internal/storage"
)

// v1SettingsDeps wires the per-server settings/INI API.
//
//	GET   /api/v1/servers/{name}/settings           — list .ini files
//	GET   /api/v1/servers/{name}/settings/{file}    — read one INI
//	PUT   /api/v1/servers/{name}/settings/{file}    — replace or merge
//	POST  /api/v1/servers/{name}/settings/sync-bans — write global ban
//	                                                list into BannedIDs.ini
type v1SettingsDeps struct {
	store   *storage.Store
	manager *server.Manager
	bans    *bans.Store
}

func (d *v1SettingsDeps) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	parts := strings.SplitN(path, "/", 2)
	serverName := parts[0]
	rest := ""
	if len(parts) > 1 {
		rest = parts[1]
	}

	if rest == "" {
		switch r.Method {
		case http.MethodGet:
			d.list(w, r, serverName)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	if rest == "sync-bans" {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		d.syncBans(w, r, serverName)
		return
	}
	// rest is an ini filename
	relPath := rest
	switch r.Method {
	case http.MethodGet:
		d.readINI(w, r, serverName, relPath)
	case http.MethodPut:
		d.writeINI(w, r, serverName, relPath)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (d *v1SettingsDeps) installDirFor(r *http.Request, serverName string) (string, error) {
	srv, err := d.store.Servers().GetServer(r.Context(), serverName)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return "", fmt.Errorf("server not found")
		}
		return "", err
	}
	return srv.InstallDir, nil
}

func (d *v1SettingsDeps) list(w http.ResponseWriter, r *http.Request, serverName string) {
	installDir, err := d.installDirFor(r, serverName)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, err.Error())
		return
	}
	files, err := settings.ListINI(installDir)
	if err != nil {
		log.Printf("list ini: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	rels := make([]string, len(files))
	for i, f := range files {
		rels[i] = filepath.Base(f)
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": rels})
}

func (d *v1SettingsDeps) readINI(w http.ResponseWriter, r *http.Request, serverName, relPath string) {
	installDir, err := d.installDirFor(r, serverName)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, err.Error())
		return
	}
	if !isSafeINI(relPath) {
		writeJSONError(w, http.StatusBadRequest, "invalid filename: must be a single .ini file")
		return
	}
	fullPath := filepath.Join(installDir, relPath)
	// Defensive: ensure the resolved path is still inside installDir.
	abs, err := filepath.Abs(fullPath)
	absDir, _ := filepath.Abs(installDir)
	if err != nil || !strings.HasPrefix(abs, absDir+string(filepath.Separator)) {
		writeJSONError(w, http.StatusBadRequest, "invalid path")
		return
	}
	f, err := settings.Parse(fullPath)
	if err != nil {
		log.Printf("parse ini: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	f.RelPath = relPath
	writeJSON(w, http.StatusOK, f)
}

func (d *v1SettingsDeps) writeINI(w http.ResponseWriter, r *http.Request, serverName, relPath string) {
	installDir, err := d.installDirFor(r, serverName)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, err.Error())
		return
	}
	if !isSafeINI(relPath) {
		writeJSONError(w, http.StatusBadRequest, "invalid filename: must be a single .ini file")
		return
	}
	var body settings.File
	body.Path = filepath.Join(installDir, relPath)
	body.RelPath = relPath
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := body.Write(); err != nil {
		log.Printf("write ini: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (d *v1SettingsDeps) syncBans(w http.ResponseWriter, r *http.Request, serverName string) {
	installDir, err := d.installDirFor(r, serverName)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, err.Error())
		return
	}
	ids, err := d.bans.ListSteamIDs(r.Context())
	if err != nil {
		log.Printf("list ban steam ids: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := settings.SyncBannedIDs(installDir, ids); err != nil {
		log.Printf("sync banned IDs: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"ban_count": len(ids),
		"path":      filepath.Join(installDir, "BannedIDs.ini"),
	})
}

// isSafeINI allows only simple basenames ending in .ini. No slashes,
// no '..', no NUL. Conservative on purpose — settings files are
// sensitive and we don't need path traversal.
func isSafeINI(name string) bool {
	if name == "" || len(name) > 255 {
		return false
	}
	if strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") || strings.Contains(name, "\x00") {
		return false
	}
	return strings.HasSuffix(strings.ToLower(name), ".ini")
}
