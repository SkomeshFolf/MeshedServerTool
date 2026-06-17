// Package api wires HTTP routes for the MeshedServerTool v3 backend.
//
// Phase 0: just a health endpoint and a static-file fallback for the embedded
// React build. Phase 1+ adds /api/v1/auth, /api/v1/servers, etc.
package api

import (
	"io/fs"
	"log"
	"net/http"
	"strings"

	"github.com/Skomesh/MeshedServerTool/internal/storage"
	"github.com/Skomesh/MeshedServerTool/web"
)

// staticSubFS exposes the web package's embed.FS to the api package.
// Defined here as a thin wrapper so the api package can stay clean of
// the embed directive itself.
func staticSubFS() (fs.FS, error) {
	return web.DistFS()
}

// NewRouter constructs the HTTP handler. The dataDir is used to locate
// static assets once they're embedded (see web/embed.go).
func NewRouter(store *storage.Store, dataDir string) http.Handler {
	mux := http.NewServeMux()

	// Health endpoint (used by orchestrators, also a quick smoke test)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","version":"v3-dev"}`))
	})

	// API root placeholder. Phase 1+ mounts /api/v1/... subroutes here.
	mux.HandleFunc("/api/v1/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not implemented: "+r.URL.Path, http.StatusNotImplemented)
	})

	// Static assets (React build) — see web/embed.go.
	staticFS, err := staticSubFS()
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
	// Only handle non-API routes (router mux already claimed /healthz and /api/)
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

	// Set content type for common extensions
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
