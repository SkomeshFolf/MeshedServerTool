// Package api wires HTTP routes for the MeshedServerTool v3 backend.
package api

import (
	"io/fs"
	"log"
	"net/http"
	"strings"

	"github.com/Skomesh/MeshedServerTool/internal/auth"
	"github.com/Skomesh/MeshedServerTool/internal/storage"
	"github.com/Skomesh/MeshedServerTool/web"
)

// NewRouter constructs the HTTP handler.
func NewRouter(store *storage.Store, dataDir string) http.Handler {
	mux := http.NewServeMux()

	// Health endpoint (used by orchestrators, also a quick smoke test)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","version":"v3-dev"}`))
	})

	// Mount /api/v1 subrouter
	authSvc := auth.NewService(store)
	deps := &v1AuthDeps{svc: authSvc, store: store}
	mux.Handle("/api/v1/auth/", http.StripPrefix("/api/v1/auth", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /api/v1/auth/  →  /
		route := strings.TrimPrefix(r.URL.Path, "/")
		// Auth middleware applies to /me only; everything else is public
		// so the login page works before the user has a session.
		if route == "me" {
			authSvc.Middleware(http.HandlerFunc(deps.handleMe)).ServeHTTP(w, r)
			return
		}
		switch route {
		case "login":
			deps.handleLogin(w, r)
		case "logout":
			deps.handleLogout(w, r)
		case "bootstrap":
			deps.handleBootstrap(w, r)
		case "status":
			deps.handleStatus(w, r)
		default:
			http.Error(w, "not found: "+r.URL.Path, http.StatusNotFound)
		}
	})))

	// Catch-all for unmounted /api/* — keep the 501 contract from Phase 0
	// so it's obvious which routes are not yet implemented.
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
		// Fall back to index.html so React Router can handle the route
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
