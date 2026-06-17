// Package api wires HTTP routes for the MeshedServerTool v3 backend.
package api

import (
	"io/fs"
	"log"
	"net/http"
	"strings"

	"github.com/Skomesh/MeshedServerTool/internal/auth"
	"github.com/Skomesh/MeshedServerTool/internal/server"
	"github.com/Skomesh/MeshedServerTool/internal/storage"
	"github.com/Skomesh/MeshedServerTool/web"
)

// NewRouter constructs the HTTP handler.
func NewRouter(store *storage.Store, manager *server.Manager, dataDir string) http.Handler {
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
	stripped := http.StripPrefix("/api/v1/servers", serverDeps)
	mux.Handle("/api/v1/servers/", authSvc.Middleware(stripped))
	mux.Handle("/api/v1/servers", authSvc.Middleware(stripped))

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
