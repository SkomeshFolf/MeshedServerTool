package api

import (
	"log"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/Skomesh/MeshedServerTool/internal/chat"
	"github.com/Skomesh/MeshedServerTool/internal/server"
	"github.com/Skomesh/MeshedServerTool/internal/storage"
)

// v1AggregateDeps wires the cross-server aggregate endpoints.
// Each endpoint gets its own dispatch closure in the router so we
// avoid the http.StripPrefix ambiguity when the trailing slash
// is or isn't present.
type v1AggregateDeps struct {
	manager *server.Manager
	store   *storage.Store
}

// LogEntry is one log line plus the server it came from. The buffer's
// own Line type doesn't carry server_name; we wrap it here so the
// React /logs page can label and color each line.
type LogEntry struct {
	ServerName string    `json:"server_name"`
	Raw        string    `json:"raw"`
	Type       string    `json:"type"`
	Fields     any       `json:"fields,omitempty"`
	At         time.Time `json:"at"`
}

// handleAggregateLogs returns the last N lines from every server's
// log buffer, merged and sorted by timestamp.
//
//	GET /api/v1/logs?tail=200       — JSON {entries: [...]} newest-first
//	GET /api/v1/logs?server=alpha   — filter to one server (optional)
func (d *v1AggregateDeps) handleAggregateLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	tail, _ := strconv.Atoi(q.Get("tail"))
	if tail <= 0 {
		tail = 200
	}
	if tail > 5000 {
		tail = 5000
	}
	onlyServer := q.Get("server")

	servers := d.manager.AllServers()
	if onlyServer != "" {
		var filtered []*server.Server
		for _, s := range servers {
			if s.Config().Name == onlyServer {
				filtered = append(filtered, s)
				break
			}
		}
		servers = filtered
	}

	out := make([]LogEntry, 0, tail)
	for _, srv := range servers {
		lines := srv.LogBuffer().Snapshot(tail)
		for _, l := range lines {
			out = append(out, LogEntry{
				ServerName: srv.Config().Name,
				Raw:        l.Text,
				Type:       l.Type,
				Fields:     l.Fields,
				At:         l.At,
			})
		}
	}
	// Newest first; ties broken by server name for stability.
	sort.Slice(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.After(out[j].At)
		}
		return out[i].ServerName < out[j].ServerName
	})
	if len(out) > tail {
		out = out[:tail]
	}
	if out == nil {
		out = []LogEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out})
}

// ChatEntry wraps chat.Message with the server name. (chat.Message
// already has ServerName, but we expose it here consistently with
// LogEntry.)
type ChatEntry struct {
	ID         int64     `json:"id"`
	ServerName string    `json:"server_name"`
	PlayerName string    `json:"player_name"`
	Message    string    `json:"message"`
	At         time.Time `json:"at"`
}

// handleAggregateChats returns recent chat messages across all
// (or one) servers.
//
//	GET /api/v1/chats?limit=200      — newest-first across every server
//	GET /api/v1/chats?server=alpha   — filter to one server
//	GET /api/v1/chats?since=ID       — only messages newer than ID
func (d *v1AggregateDeps) handleAggregateChats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 200
	}
	if limit > 5000 {
		limit = 5000
	}
	since, _ := strconv.ParseInt(q.Get("since"), 10, 64)
	onlyServer := q.Get("server")

	chatStore := chat.New(d.store)
	servers := d.manager.AllServers()

	want := map[string]bool{}
	if onlyServer != "" {
		want[onlyServer] = true
	} else {
		for _, s := range servers {
			want[s.Config().Name] = true
		}
	}

	out := make([]ChatEntry, 0, limit)
	for name := range want {
		msgs, err := chatStore.List(r.Context(), name, limit)
		if err != nil {
			log.Printf("aggregate chats: list %s: %v", name, err)
			continue
		}
		for _, m := range msgs {
			if since > 0 && m.ID <= since {
				continue
			}
			out = append(out, ChatEntry{
				ID:         m.ID,
				ServerName: m.ServerName,
				PlayerName: m.PlayerName,
				Message:    m.Message,
				At:         m.At,
			})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.After(out[j].At)
		}
		return out[i].ID > out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	if out == nil {
		out = []ChatEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out})
}
