package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/Skomesh/MeshedServerTool/internal/auth"
	"github.com/Skomesh/MeshedServerTool/internal/motd"
	"github.com/Skomesh/MeshedServerTool/internal/storage"
)

// v1MotdDeps wires the MOTD API.
type v1MotdDeps struct {
	store *motd.Store
}

// ServeHTTP routes /api/v1/motd and /api/v1/motd/{serverName}.
//
//	GET    /api/v1/motd                       — list all per-server MOTDs + global default
//	GET    /api/v1/motd/{serverName}          — effective MOTD for a server (per-server OR global)
//	PUT    /api/v1/motd/{serverName}          — set per-server MOTD
//	DELETE /api/v1/motd/{serverName}          — remove per-server MOTD override
//	PUT    /api/v1/motd (with body server_name="*") — set global default
func (d *v1MotdDeps) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		switch r.Method {
		case http.MethodGet:
			d.list(w, r)
		case http.MethodPut:
			d.setGlobal(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	switch r.Method {
	case http.MethodGet:
		d.getForServer(w, r, path)
	case http.MethodPut:
		d.setForServer(w, r, path)
	case http.MethodDelete:
		d.deleteForServer(w, r, path)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (d *v1MotdDeps) list(w http.ResponseWriter, r *http.Request) {
	all, err := d.store.List(r.Context())
	if err != nil {
		log.Printf("list motd: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if all == nil {
		all = []*motd.MOTD{}
	}
	global, err := d.store.GetGlobal(r.Context())
	if err != nil {
		log.Printf("get global motd: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if global == nil {
		global = &motd.MOTD{Message: "", Enabled: false, ServerName: "*"}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"global":   global,
		"servers":  all,
	})
}

func (d *v1MotdDeps) getForServer(w http.ResponseWriter, r *http.Request, serverName string) {
	m, fromServer, err := d.store.GetForServer(r.Context(), serverName)
	if err != nil {
		log.Printf("get motd: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"message":      m,
		"per_server":   fromServer,
	})
}

func (d *v1MotdDeps) setGlobal(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	updatedBy := ""
	if u := auth.UserFromContext(r.Context()); u != nil {
		updatedBy = u.Username
	}
	m, err := d.store.SetGlobal(r.Context(), body.Message, updatedBy)
	if err != nil {
		log.Printf("set global motd: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (d *v1MotdDeps) setForServer(w http.ResponseWriter, r *http.Request, serverName string) {
	var body struct {
		Message string `json:"message"`
		Enabled *bool  `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	updatedBy := ""
	if u := auth.UserFromContext(r.Context()); u != nil {
		updatedBy = u.Username
	}
	m, err := d.store.SetForServer(r.Context(), serverName, body.Message, enabled, updatedBy)
	if err != nil {
		log.Printf("set motd: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (d *v1MotdDeps) deleteForServer(w http.ResponseWriter, r *http.Request, serverName string) {
	if err := d.store.DeleteForServer(r.Context(), serverName); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "not found")
			return
		}
		log.Printf("delete motd: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
