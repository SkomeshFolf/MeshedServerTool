// Package api wires HTTP routes for the MeshedServerTool v3 backend.
package api

import (
	"io/fs"
	"log"
	"net/http"
	"strings"

	"github.com/Skomesh/MeshedServerTool/internal/auth"
	"github.com/Skomesh/MeshedServerTool/internal/bans"
	"github.com/Skomesh/MeshedServerTool/internal/chat"
	"github.com/Skomesh/MeshedServerTool/internal/hub"
	"github.com/Skomesh/MeshedServerTool/internal/motd"
	"github.com/Skomesh/MeshedServerTool/internal/reports"
	"github.com/Skomesh/MeshedServerTool/internal/server"
	"github.com/Skomesh/MeshedServerTool/internal/storage"
	"github.com/Skomesh/MeshedServerTool/web"
)

// NewRouter constructs the HTTP handler.
func NewRouter(store *storage.Store, manager *server.Manager, h *hub.Hub, reportsStore *reports.Store, bansStore *bans.Store, chatStore *chat.Store, motdStore *motd.Store, dataDir string) http.Handler {
	mux := http.NewServeMux()

	// Health endpoint (used by orchestrators, also a quick smoke test)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","version":"v3-dev"}`))
	})

	// Mount /api/v1 subrouter
	authSvc := auth.NewService(store)
	authDeps := &v1AuthDeps{svc: authSvc, store: store}
	serverDeps := &v1ServerDeps{store: store, manager: manager}
	reportsDeps := &v1ReportsDeps{store: reportsStore, hub: h}
	bansDeps := &v1BansDeps{store: bansStore, hub: h}
	chatDeps := &v1ChatDeps{store: chatStore}
	aggregateDeps := &v1AggregateDeps{manager: manager, store: store}
	logsHandler := http.HandlerFunc(aggregateDeps.handleAggregateLogs)
	chatsHandler := http.HandlerFunc(aggregateDeps.handleAggregateChats)
	wsDeps := &v1WebSocketDeps{hub: h}
	motdDeps := &v1MotdDeps{store: motdStore}
	settingsDeps := &v1SettingsDeps{store: store, manager: manager, bans: bansStore}

	mux.Handle("/api/v1/auth/", http.StripPrefix("/api/v1/auth", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := strings.TrimPrefix(r.URL.Path, "/")
		if route == "me" {
			authSvc.Middleware(http.HandlerFunc(authDeps.handleMe)).ServeHTTP(w, r)
			return
		}
		switch route {
		case "login":
			authDeps.handleLogin(w, r)
		case "logout":
			authDeps.handleLogout(w, r)
		case "bootstrap":
			authDeps.handleBootstrap(w, r)
		case "status":
			authDeps.handleStatus(w, r)
		default:
			http.Error(w, "not found: "+r.URL.Path, http.StatusNotFound)
		}
	})))

	// /api/v1/servers...  — all auth-protected.
	// Register both /api/v1/servers/ and /api/v1/servers (no trailing
	// slash) so clients don't get a 301 redirect to the trailing-slash
	// form. http.ServeMux treats them as distinct patterns.
	//
	// The handler dispatches based on path. The path-matching is:
	//   /api/v1/servers/{name}/settings          → settingsDeps.ServeHTTP
	//   /api/v1/servers/{name}/settings/{file}   → settingsDeps.ServeHTTP (after /settings stripped)
	//   /api/v1/servers/{name}/settings/sync-bans → settingsDeps.ServeHTTP
	//   everything else under /api/v1/servers/*  → serverDeps.ServeHTTP
	//
	// We strip the /settings prefix before handing to settingsDeps so
	// settingsDeps sees the path it expects (e.g. "ini-srv/BannedIDs.ini").
	serversDispatcher := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/settings") {
			// r.URL.Path has had "/api/v1/servers" stripped by http.StripPrefix above,
			// so it's now like "ini-srv/settings/BannedIDs.ini" or "ini-srv/settings".
			// Re-prefix the server name so settingsDeps can split it.
			// Actually, easier: write the path back with /api/v1/servers prepended,
			// then re-strip just /api/v1/servers/{name}/settings.
			// We have the server name from the first path segment.
			trimmed := strings.TrimPrefix(r.URL.Path, "/")
			parts := strings.SplitN(trimmed, "/", 2)
			serverName := parts[0]
			rest := ""
			if len(parts) > 1 {
				rest = parts[1]
			}
			// rest is "settings", "settings/BannedIDs.ini", or "settings/sync-bans"
			subRest := strings.TrimPrefix(rest, "settings")
			subRest = strings.TrimPrefix(subRest, "/")
			// Build a new path: "{serverName}" + "/" + subRest
			newPath := "/" + serverName
			if subRest != "" {
				newPath += "/" + subRest
			}
			r2 := r.Clone(r.Context())
			r2.URL.Path = newPath
			settingsDeps.ServeHTTP(w, r2)
			return
		}
		serverDeps.ServeHTTP(w, r)
	})
	stripped := http.StripPrefix("/api/v1/servers", serversDispatcher)
	mux.Handle("/api/v1/servers/", authSvc.Middleware(stripped))
	mux.Handle("/api/v1/servers", authSvc.Middleware(stripped))

	// /api/v1/ws — WebSocket endpoint. Auth-protected; same cookie as
	// REST. Streams all hub events to the client.
	mux.Handle("/api/v1/ws", authSvc.Middleware(http.HandlerFunc(wsDeps.handleWebSocket)))

	// /api/v1/reports — list/create (POST), single (GET/PATCH/DELETE).
	mux.Handle("/api/v1/reports/",
		authSvc.Middleware(http.StripPrefix("/api/v1/reports", reportsDeps)))
	mux.Handle("/api/v1/reports",
		authSvc.Middleware(http.StripPrefix("/api/v1/reports", reportsDeps)))

	// /api/v1/bans — global ban list CRUD.
	mux.Handle("/api/v1/bans/",
		authSvc.Middleware(http.StripPrefix("/api/v1/bans", bansDeps)))
	mux.Handle("/api/v1/bans",
		authSvc.Middleware(http.StripPrefix("/api/v1/bans", bansDeps)))

	// /api/v1/chat — per-server chat history.
	mux.Handle("/api/v1/chat/",
		authSvc.Middleware(http.StripPrefix("/api/v1/chat", chatDeps)))

	// /api/v1/logs, /api/v1/chats — cross-server aggregate views.
	mux.Handle("/api/v1/logs/",
		authSvc.Middleware(logsHandler))
	mux.Handle("/api/v1/logs",
		authSvc.Middleware(logsHandler))
	mux.Handle("/api/v1/chats/",
		authSvc.Middleware(chatsHandler))
	mux.Handle("/api/v1/chats",
		authSvc.Middleware(chatsHandler))

	// /api/v1/motd — message of the day.
	mux.Handle("/api/v1/motd/",
		authSvc.Middleware(http.StripPrefix("/api/v1/motd", motdDeps)))
	mux.Handle("/api/v1/motd",
		authSvc.Middleware(http.StripPrefix("/api/v1/motd", motdDeps)))

	// Catch-all for unmounted /api/* — keep the 501 contract from Phase 0
	mux.HandleFunc("/api/v1/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not implemented: "+r.URL.Path, http.StatusNotImplemented)
	})

	// Static assets (React build) — see web/embed.go.
	staticFS, err := web.DistFS()
	if err != nil {
		log.Printf("warning: static assets not embedded yet: %v", err)
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "frontend not built — run `npm run build` in web/ first", http.StatusServiceUnavailable)
		})
		return mux
	}
	mux.Handle("/", spaHandler{staticFS: staticFS, dataDir: dataDir})

	return mux
}

// spaHandler serves embedded static assets and falls back to index.html
// for client-side routes (the React Router pattern).
type spaHandler struct {
	staticFS fs.FS
	dataDir  string
}

func (h spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	f, err := h.staticFS.Open(path)
	if err != nil {
		f, err = h.staticFS.Open("index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
	}
	defer f.Close()

	stat, _ := f.Stat()
	if stat != nil && !stat.IsDir() {
		switch {
		case strings.HasSuffix(path, ".html"):
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		case strings.HasSuffix(path, ".js"):
			w.Header().Set("Content-Type", "application/javascript")
		case strings.HasSuffix(path, ".css"):
			w.Header().Set("Content-Type", "text/css")
		case strings.HasSuffix(path, ".svg"):
			w.Header().Set("Content-Type", "image/svg+xml")
		}
	}
	http.ServeContent(w, r, path, stat.ModTime(), readSeeker(f))
}
