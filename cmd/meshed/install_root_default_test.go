package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestInstallRoot_DefaultAllowsServerCreate verifies that when the
// operator runs `./meshed` with no flags, server-create works as long
// as install_dir lives under <data-dir>/servers.
//
// Regression: before this fix, --install-root defaulted to empty,
// which rejected every install_dir. A user running `./meshed` on a
// fresh box couldn't create a server without first discovering and
// setting a flag that the help text described as "required in
// production". That was a footgun.
func TestInstallRoot_DefaultAllowsServerCreate(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh not available")
	}

	// Build the binary once for the test.
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "meshed")
	build := exec.Command("go", "build", "-o", binPath, ".")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	// Fresh data dir.
	dataDir := t.TempDir()

	// Pick a free port.
	port := freePort(t)

	// Start the binary with NO flags other than -addr and -data-dir.
	// In particular: no --install-root.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, binPath,
		"-addr", fmt.Sprintf("127.0.0.1:%d", port),
		"-data-dir", dataDir,
		"-log-level", "error",
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatalf("start meshed: %v", err)
	}
	defer func() {
		cancel()
		_ = cmd.Wait()
	}()

	// Wait for the binary to bind.
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	if !waitForHealthz(base, 5*time.Second) {
		t.Fatal("meshed did not come up within 5s")
	}

	// Bootstrap (creates first user).
	cookie := bootstrapFirstUser(t, base, "admin", "correcthorse")
	if cookie == "" {
		t.Fatal("bootstrap did not return a session cookie")
	}

	// Confirm the data-dir/servers directory was auto-created.
	serversDir := filepath.Join(dataDir, "servers")
	if fi, err := os.Stat(serversDir); err != nil {
		t.Fatalf("expected %s to exist (auto-created by --install-root default), got: %v", serversDir, err)
	} else if fi.Mode().Perm() != 0o700 {
		t.Errorf("servers dir mode = %o, want 0o700", fi.Mode().Perm())
	}

	// Create a server under the default install_root. Should succeed.
	good := map[string]any{
		"name":        "good",
		"install_dir": filepath.Join(serversDir, "good"),
		"port":        7777,
		"args": map[string]any{
			"executable": "/bin/sh",
			"argv":       []string{"-c", "echo hi"},
		},
	}
	if status, body := createServer(t, base, cookie, good); status != http.StatusCreated {
		t.Fatalf("good install_dir (%s) should succeed (got %d): %s",
			good["install_dir"], status, body)
	}

	// Reserved path: /etc/x should still fail.
	bad := map[string]any{
		"name":        "bad",
		"install_dir": "/etc/x",
		"port":        7778,
	}
	if status, body := createServer(t, base, cookie, bad); status != http.StatusBadRequest {
		t.Errorf("/etc/x should be rejected (got %d): %s", status, body)
	}

	// Outside default install_root: /home/skomesh/x should fail.
	outside := map[string]any{
		"name":        "outside",
		"install_dir": "/tmp/somewhere-outside-root",
		"port":        7779,
	}
	if status, body := createServer(t, base, cookie, outside); status != http.StatusBadRequest {
		t.Errorf("outside-install_root should be rejected (got %d): %s", status, body)
	}
}

// --- helpers ---

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func waitForHealthz(base string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return true
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func bootstrapFirstUser(t *testing.T, base, user, pass string) string {
	t.Helper()
	body := fmt.Sprintf(`{"username":%q,"password":%q}`, user, pass)
	resp, err := http.Post(base+"/api/v1/auth/bootstrap", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("bootstrap status %d: %s", resp.StatusCode, b)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "meshed_session" {
			return c.Value
		}
	}
	// Some configurations don't set a cookie on bootstrap (depends on
	// auth handler). In that case log in.
	return login(t, base, user, pass)
}

func login(t *testing.T, base, user, pass string) string {
	t.Helper()
	body := fmt.Sprintf(`{"username":%q,"password":%q}`, user, pass)
	resp, err := http.Post(base+"/api/v1/auth/login", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("login status %d: %s", resp.StatusCode, b)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "meshed_session" {
			return c.Value
		}
	}
	t.Fatal("no session cookie in login response")
	return ""
}

func createServer(t *testing.T, base, cookie string, body map[string]any) (int, string) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", base+"/api/v1/servers", strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "meshed_session", Value: cookie})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}
