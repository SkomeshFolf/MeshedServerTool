package api

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Skomesh/MeshedServerTool/internal/auth"
	"github.com/Skomesh/MeshedServerTool/internal/storage"
)

// v1BackupDeps wires the admin backup endpoint.
//
// GET /api/v1/admin/backup — auth-gated, role-gated. Streams a
// consistent snapshot of the SQLite database to the client as
// `application/octet-stream` with a timestamped filename. The file
// is produced by VACUUM INTO into the data dir, then served via
// http.ServeFile. (audit finding M13)
type v1BackupDeps struct {
	store   *storage.Store
	dataDir string
}

func (d *v1BackupDeps) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Auth middleware already verified the session. We additionally
	// require the admin role — backups expose the full DB (users,
	// sessions, server config). v1AuthDeps.handleMe populates the
	// role; we look it up from the request context via auth.UserFromContext.
	if !isAdmin(r) {
		http.Error(w, "forbidden: admin role required", http.StatusForbidden)
		return
	}

	// Build a unique path under the data dir. We suffix a random hex
	// string so concurrent backup requests don't collide.
	nonce := make([]byte, 6)
	if _, err := rand.Read(nonce); err != nil {
		http.Error(w, "internal: cannot generate nonce", http.StatusInternalServerError)
		return
	}
	ts := time.Now().UTC().Format("20060102-150405")
	filename := "meshed-backup-" + ts + "-" + hex.EncodeToString(nonce) + ".db"
	destPath := filepath.Join(d.dataDir, filename)

	if err := d.store.Backup(destPath); err != nil {
		log.Printf("backup: %v", err)
		http.Error(w, "backup failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// Best-effort cleanup. We serve the file with http.ServeFile,
	// which doesn't expose a hook for "after send, delete the
	// file", so we register a goroutine that waits long enough for
	// the response to flush, then removes the file. The client is
	// expected to save the download before then.
	defer func() {
		go func(path string) {
			time.Sleep(2 * time.Minute)
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				log.Printf("backup: cleanup %s: %v", path, err)
			}
		}(destPath)
	}()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	http.ServeFile(w, r, destPath)
}

// isAdmin returns true when the request was made by an authenticated
// user with role=admin. The auth middleware sets the user on the
// request context; we look it up via auth.UserFromContext.
func isAdmin(r *http.Request) bool {
	user := auth.UserFromContext(r.Context())
	return user != nil && user.Role == "admin"
}
