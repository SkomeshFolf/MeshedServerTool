package api

import (
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/Skomesh/MeshedServerTool/internal/chat"
)

// v1ChatDeps wires the per-server chat history API.
type v1ChatDeps struct {
	store *chat.Store
}

// ServeHTTP routes /api/v1/chat/{serverName}.
//
//	GET /api/v1/chat/{serverName}?limit=200
func (d *v1ChatDeps) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	serverName := strings.TrimPrefix(r.URL.Path, "/")
	if serverName == "" {
		http.Error(w, "server name required", http.StatusBadRequest)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit == 0 {
		limit = 200
	}
	msgs, err := d.store.List(r.Context(), serverName, limit)
	if err != nil {
		log.Printf("list chat: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if msgs == nil {
		msgs = []*chat.Message{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": msgs})
}
