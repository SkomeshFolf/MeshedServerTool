// Package web exposes the embedded React build assets.
//
// In dev, web/dist may not exist; staticSubFS returns an error and the API
// serves a helpful "build the frontend" message. In release builds, the
// Vite output is embedded directly into the Go binary, so the deployed
// artifact is a single file.
package web

import (
	"embed"
	"errors"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// DistFS returns an fs.FS rooted at web/dist, ready to be served by the API.
//
// Returns an error if dist doesn't exist (i.e. `npm run build` hasn't been
// run yet). Callers should handle that gracefully during dev.
func DistFS() (fs.FS, error) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, err
	}
	// Sanity check: dist must contain index.html
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, errors.New("web/dist missing or incomplete — run `npm run build` in web/ first")
	}
	return sub, nil
}
