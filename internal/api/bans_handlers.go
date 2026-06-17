package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/Skomesh/MeshedServerTool/internal/auth"
	"github.com/Skomesh/MeshedServerTool/internal/bans"
	"github.com/Skomesh/MeshedServerTool/internal/hub"
	"github.com/Skomesh/MeshedServerTool/internal/storage"
)

// v1BansDeps wires the global ban list API.
type v1BansDeps struct {
	store *bans.Store
	hub   *hub.Hub
}

// ServeHTTP routes /api/v1/bans and /api/v1/bans/{id}.
//
//	GET    /api/v1/bans
//	POST   /api/v1/bans  {steam_id, player_name, reason}
//	DELETE /api/v1/bans/{id}
func (d *v1BansDeps) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	parts := strings.SplitN(path, "/", 2)
	idStr := parts[0]
	rest := ""
	if len(parts) > 1 {
		rest = parts[1]
	}

	if idStr == "" {
		switch r.Method {
		case http.MethodGet:
			d.list(w, r)
		case http.MethodPost:
			d.add(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	if rest != "" {
		http.Error(w, "not found: "+r.URL.Path, http.StatusNotFound)
		return
	}
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	d.remove(w, r, id)
}

func (d *v1BansDeps) list(w http.ResponseWriter, r *http.Request) {
	list, err := d.store.List(r.Context())
	if err != nil {
		log.Printf("list bans: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if list == nil {
		list = []*bans.Ban{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"bans": list})
}

func (d *v1BansDeps) add(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SteamID    string `json:"steam_id"`
		PlayerName string `json:"player_name"`
		Reason     string `json:"reason"`
		BannedBy   string `json:"banned_by"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.SteamID == "" {
		writeJSONError(w, http.StatusBadRequest, "steam_id is required")
		return
	}
	// Get the banning user from the auth context.
	if body.BannedBy == "" {
		if u := auth.UserFromContext(r.Context()); u != nil {
			body.BannedBy = u.Username
		}
	}
	ban, err := d.store.Add(r.Context(), body.SteamID, body.PlayerName, body.Reason, body.BannedBy)
	if err != nil {
		log.Printf("add ban: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	d.hub.Publish(hub.Event{Type: "ban.added", Data: ban})
	writeJSON(w, http.StatusOK, ban)
}

func (d *v1BansDeps) remove(w http.ResponseWriter, r *http.Request, id int64) {
	if err := d.store.Remove(r.Context(), id); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	d.hub.Publish(hub.Event{Type: "ban.removed", Data: id})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
