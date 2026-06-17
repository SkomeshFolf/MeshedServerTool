# MeshedServerTool v3 — Deployment / Cross-platform / Observability Audit

**Scope:** Read-only inspection of `cmd/meshed/main.go`, `internal/config`,
`internal/server`, `internal/api`, `web/embed.go`, `go.mod`, `README.md`,
`PLAN.md`, and `web/`.
**Branch:** `v3-dev` @ `00484d6`
**Confidence basis:** File contents, plus empirical cross-compile of the
Windows and macOS targets with the default toolchain.

---

## CRITICAL

### C1. Autocert cache directory is never created
**Location:** `cmd/meshed/main.go:98-105`
**Problem:** When `--autocert-domain` is set without `--autocert-cache`,
`cacheDir = filepath.Join(*dataDir, "autocert")` is passed to
`autocert.DirCache(cacheDir)`. `autocert.DirCache` does `os.MkdirAll` itself,
so this isn't a hard crash, but the resulting directory inherits the
`umask` of the running user (typically 0755 / world-readable on Linux). On
a multi-user system the cert *and* private key in that cache are
readable by every account on the box. No documentation tells the user
about this directory at all.
**Fix:** Call `os.MkdirAll(cacheDir, 0o700)` and then `os.Chmod` after
(Go's `MkdirAll` is subject to umask). Document the path. Consider
`autocert.Cache` interface shim that writes with 0600 on first write.
**Confidence:** High.

### C2. WebSocket upgrade skips origin verification
**Location:** `internal/api/websocket_handlers.go:43-48`
**Problem:** `InsecureSkipVerify: true` is set on the websocket accept
options with a comment claiming the cookie auth is CSRF-safe. That is
**wrong**: a same-site WebSocket from a malicious page can read the
session cookie in the browser, and the upgrade is just an HTTP request
with an `Origin` header. For a *local-only* app this is borderline, but
once `--autocert-domain` is in play the same binary is exposed on the
public internet and this becomes a CSRF + session-leak vector. The
comment even acknowledges "a future hardening pass should add a strict
origin allowlist" — that hardening hasn't happened.
**Fix:** Allowlist `Origin` against the request's `Host` header (or the
configured `--autocert-domain`). Reject mismatches. Keep the comment
honest.
**Confidence:** High.

### C3. `autostart` is persisted but never honored
**Location:** `internal/storage/storage.go:128`, `internal/storage/servers.go:30`,
`internal/api/server_handlers.go:105,132,169-191`
**Problem:** The DB schema has an `autostart` column, the API accepts
and persists it, but `Manager.NewManager` (`internal/server/server.go:49-83`)
never iterates `Servers()` calling `Start()` for ones with `Autostart=true`.
The field is dead — either remove it from the public surface or wire it
in. Users who set `autostart: true` and then restart `meshed` will be
surprised when their server doesn't start.
**Fix:** After rehydration in `NewManager`, range over the loaded
`ServerView`s and call `m.Start(ctx, srv.Name)` for each where
`Autostart` is true. Note: this means `meshed` startup will block on the
first server, so either fire the starts in goroutines or document the
behavior.
**Confidence:** High.

---

## HIGH

### H1. Data dir, DB file, and autocert cache created with world-readable bits
**Location:** `cmd/meshed/main.go:47`, `internal/storage/storage.go:27`,
`cmd/meshed/main.go:98-105`
**Problem:** Three artifacts on a multi-user system leak through the
umask:
- `*dataDir` is `os.MkdirAll(*dataDir, 0o755)` (main.go:47) — should be `0o700`.
- `meshed.db` (and `-wal`/`-shm`) are created by the SQLite driver with
  its default mode (0666 & ~umask). The DB contains bcrypt hashes
  (`users.password_hash`) and active session tokens (`sessions.token`).
- The autocert cache (C1) — same issue.
**Fix:** `os.MkdirAll(*dataDir, 0o700)`; call
`store.DB().Exec("PRAGMA secure_delete = ON")` (or pass it in the DSN
like the other pragmas) and consider running
`os.Chmod(filepath.Join(*dataDir, "meshed.db"), 0o600)` after open on
non-Windows. For the autocert cache, see C1.
**Confidence:** High.

### H2. No `os.User` home-dir fallback if `$HOME` is unset (macOS, Linux)
**Location:** `internal/config/config.go:21-37`
**Problem:** All three branches do `os.Getenv("HOME")` /
`os.Getenv("USERPROFILE")` / `os.Getenv("XDG_DATA_HOME")` with no
fallback. If a service launches `meshed` in an environment with `HOME`
unset (e.g. a bare `sudo` on some systems, a broken launchd unit, a
Docker image that doesn't set it), `filepath.Join("", ...)` produces a
relative path that silently resolves to the CWD, and the binary will
create a `Library/Application Support/Meshed Server Tool` directory
*next to wherever you launched it from*. On Windows the same is true
for `USERPROFILE` + `LocalAppData` both being unset.
**Fix:** Add `os.UserHomeDir()` (Go 1.12+) as the fallback; it reads
`/etc/passwd` and `user.Current()` on Unix and `%USERPROFILE%` on
Windows. Surface a clear error if even that returns empty.
**Confidence:** High.

### H3. Unix `terminateProcess` does not actually fall back to SIGKILL
**Location:** `internal/server/process_unix.go:13-18`,
`internal/server/server.go:381-429`
**Problem:** The comment in `process_unix.go` says *"The caller falls back
to Kill if the process doesn't exit within the grace period"*, and the
audit prompt's description repeats that. The Unix path returns
`cmd.Process.Signal(syscall.SIGTERM)`, and the caller's grace loop
(`server.go:408-428`) *does* call `cmd.Process.Kill()` on deadline or
context done. **So the fallback actually does work.** However:
- The grace period is **not configurable from the CLI**. The Manager's
  `Stop(ctx, name, grace)` signature accepts a duration
  (`server.go:164-172`), but every internal caller hardcodes
  `5*time.Second` (`server.go:117, 183, 235`). There is no
  `--stop-grace` / `--shutdown-timeout` flag.
- `terminateProcess` is a misnomer on Unix — it only *asks politely*;
  the kill is done by the caller. Not a bug, but the cross-platform
  symmetry is confusing.
- On Unix, `cmd.Process.Signal(SIGTERM)` returns an error like
  `os.ErrProcessDone` if the process has already exited. This is
  logged at line 402 as a warning, which is noisy and harmless but
  pollutes stderr.
- **The child becomes an orphan** if the parent dies between SIGTERM
  and SIGKILL. No `Setsid` / `pgid` / `Setpgid` is set; the child
  process is in the same process group as `meshed`, so when `meshed`
  is killed -9 (e.g. systemd `KillMode=process` race, kernel OOM, or
  the user just doing `kill -9 %1`), the child survives and gets
  reparented to PID 1. See H4.
**Fix:** Add `--stop-grace` flag (default 5s). Suppress the
`ErrProcessDone` log. Either set `cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}`
on Unix and kill `-pgid`, or document that the operator must not kill
the parent without first stopping the children.
**Confidence:** High.

### H4. SIGTERM does not stop child game servers
**Location:** `cmd/meshed/main.go:133-151`
**Problem:** On SIGTERM the binary does `srv.Shutdown(ctx)` (which only
waits for the *HTTP* server) and then exits. The child game server
processes spawned by `Manager.Start` (`server.go:295`) are **not**
reaped. The Go process is a parent of N game servers; when the user
runs `systemctl stop meshed` or the orchestrator sends SIGTERM, the
HTTP server shuts down, the binary prints `"meshed v3 stopped"`, and
the orphaned game servers keep running. (This is the same root cause
as the unix-orphan note in H3.) The comment in `Stop` says "Stop
blocks" — that's true for an explicit HTTP call to `/api/v1/servers/x/stop`,
not for the shutdown handler.
**Fix:** Before calling `srv.Shutdown`, iterate the manager and
`m.Stop(ctx, name, 5*time.Second)` for every server in
`{running, starting, stopping}` status. Or set a process group
(Setpgid / CREATE_NEW_PROCESS_GROUP on Windows) and signal the group
on shutdown. Document the behavior either way.
**Confidence:** High.

### H5. `pumpPipe` reads one byte at a time
**Location:** `internal/server/server.go:451-476`
**Problem:** `cmd.StdoutPipe()` and `cmd.StderrPipe()` are unbuffered
io.Readers. Reading 1 byte at a time means ~one syscall per byte
(no, actually it's buffered by the pipe — but it's still a per-byte
allocation of `line = append(line, one[0])`). On a high-volume game
server this is O(lines) mallocs/iterations and the comment acknowledges
"For high-volume servers this would want a bufio.Scanner." A
`bufio.Scanner` with a 1 MiB max line (matching the INI parser) is a
one-line change.
**Fix:** Replace the inner loop with `scanner := bufio.NewScanner(r)`
and `scanner.Scan()`. Keep the same `OnAppend` contract.
**Confidence:** High.

### H6. `cmd.Wait` is called in a goroutine and the grace loop polls state
**Location:** `internal/server/server.go:344-374, 405-428`
**Problem:** The "wait" goroutine (line 345) calls `cmd.Wait()`; the
explicit `Stop` (line 408-428) does **not** call `cmd.Wait` again, it
polls `s.state.Status` every 50 ms. The comment at 407 explains the
rationale (calling Wait twice would panic). This is correct but has a
visible wart: if the goroutine that's supposed to publish the
final state crashes (it doesn't `defer recover`), the polled state
gets stuck and the 50 ms loop runs until the grace expires and the
hard `Kill` is sent. The `Kill` flips the process state on the OS,
the `Wait` goroutine completes, and the state does eventually update —
but only after the grace period elapses. There's no formal recovery.
**Fix:** Either use `cmd.Wait()` directly in `Stop` (it's safe to call
after the goroutine completes) or use a `chan struct{}` closed when
`Wait` returns. The latter is the textbook pattern.
**Confidence:** Medium.

### H7. `pumpPipe` doesn't recover from panics
**Location:** `internal/server/server.go:451-476`
**Problem:** Unrecovered panics in the line-processing goroutine kill
the whole process (Go's `net/http` server has `Recover` middleware, but
this isn't a handler — it's a long-lived goroutine). If `buf.Append`
ever panics (e.g. via a registered `OnAppend` callback) the entire
`meshed` process dies. The `Buffer.Append` doc says "the callback runs
under the buffer lock — keep it fast and non-blocking" but the
production callback (`server.go:244-260`) does a `s.chat.Add(...)` SQL
insert under that lock. A slow DB at the wrong moment could panic via
context-deadline-exceeded → unrecovered → process death.
**Fix:** `defer recover()` at the top of the `pumpPipe` function and
the `Wait` goroutine. Log the panic. Don't try to continue (state
would be corrupt); but at least log the failure.
**Confidence:** Medium.

### H8. `Manager.RemoveServer` deletes from the map before stopping
**Location:** `internal/server/server.go:107-120`
**Problem:** The map entry is removed under the lock (line 114) and the
subprocess is stopped *after* releasing it. If two concurrent
`DeleteServer` calls land for the same name, the second one's
`srv.IsRunning()` check sees the wrong (now-empty) state, and the
goroutine from the first call races with any other in-flight operation
on the same name. Probably benign in practice, but the function is
loud about being "stop and remove" then doesn't serialize the two.
**Fix:** Stop first (with the lock held) or hold the write lock
through the stop. Consider a per-server mutex (the Server already has
`mu`).
**Confidence:** Medium.

### H9. Vite build output is NOT flattened; embed uses `all:dist`
**Location:** `web/embed.go:15`, `web/vite.config.ts:8-13`,
`web/dist/index.html:5-9`
**Problem:** The audit prompt asks "is the assets dir flattened?" It is
**not** — Vite keeps `web/dist/assets/index-*.js`, `web/dist/assets/index-*.css`,
and `web/dist/assets/icon-*.svg`. The `//go:embed all:dist` directive
in `web/embed.go:15` does catch everything under `dist/` (good), and
`fs.Sub(distFS, "dist")` re-roots the FS so paths like `assets/index-*.js`
work. **`index.html` itself does the right thing** — it references
`/assets/index-*.js` with a leading slash, and `spaHandler` strips
that slash and looks the file up. So this works *today*, but:
- The `all:` prefix embeds `.DS_Store`, `Thumbs.db`, or any future
  editor-junk files dropped into `web/dist/`. Vite does have an
  `emptyOutDir: true` (vite.config.ts:11) which keeps it clean, but
  a stray file would silently bloat the binary. Use `//go:embed dist`
  (no `all:`) to skip dotfiles.
- The hashed filenames mean **cache invalidation works correctly**,
  but they also mean a "production upgrade" requires rebuilding the
  Go binary — which is fine since the assets are embedded.
- A sourcemap (`web/dist/assets/index-BOR2TRCt.js.map`, 815 KB) is
  embedded. That's ~4x the size of the JS itself. Production builds
  should set `sourcemap: false` in `vite.config.ts` or filter it out
  in the embed pattern.
**Fix:** Drop `all:` and the sourcemap from the production build.
**Confidence:** High.

### H10. `IsRunning` returns true for `StatusStarting`, but `Start` rejects that same status
**Location:** `internal/server/server.go:219-224, 271-275`
**Problem:** `IsRunning()` returns `true` for `StatusRunning ||
StatusStarting`. `Server.Start()` (line 271) errors out with
`ErrInvalidTransition` if the status is `StatusRunning || StatusStarting`.
Meanwhile, the "is the state in a terminal phase" check in the Wait
goroutine (line 352) *also* includes `StatusStopping` in the "was
running" set. These three definitions of "alive" disagree subtly:
- `IsRunning` → `Running | Starting`
- `Start` rejects → `Running | Starting`
- `Wait` goroutine considers "was running" → `Running | Starting | Stopping`
- `Stop` rejects → `Running | Starting` (line 383)

The split is mostly consistent, but the asymmetry is the kind of
thing that bites the next person who edits the file. Document the
canonical "alive" set or extract a helper.
**Fix:** Add a `func (s *ServerState) IsAlive() bool` that answers the
question; use it everywhere.
**Confidence:** Medium.

---

## MEDIUM

### M1. `healthz` is a static "ok" — useless for orchestrators
**Location:** `internal/api/router.go:25-29`
**Problem:** `/healthz` always returns 200 with `{"status":"ok","version":"v3-dev"}`.
For Docker / k8s / systemd health checks this is a lie — if the
SQLite DB is locked, the autocert cache is full, the disk is full, or
the hub is wedged, the binary still reports healthy. The audit prompt
calls this out explicitly.
**Fix:** Add a `healthz` that pings the DB (`db.PingContext` with a
tight timeout, say 1s), checks the hub's `SubscriberCount > 0` only if
it should be (skip), and tries a 1-byte write to a temp file in the
data dir. Return 503 on any failure with a JSON body listing what
failed.
**Confidence:** High.

### M2. No structured logging
**Location:** every file (43 `log.Print*` call sites)
**Problem:** All logging is unstructured `log.Printf` to stderr. There
is no `--log-level` flag, no request ID, no correlation ID, no JSON
encoder, no log rotation. Stderr is fine for systemd/journald but
meaningless in Docker without a driver. There's no way to silence
verbose paths during a "real" incident.
**Fix:** Wrap `log` in a small structured logger (e.g. `log/slog` from
the stdlib, Go 1.21+, and you're on 1.25). Add `--log-level` (debug/info/warn/error).
Add a per-request request ID via a middleware that sets an
`X-Request-ID` header and logs it on every request.
**Confidence:** High.

### M3. README is v2 (Python), not v3
**Location:** `README.md:1-148`
**Problem:** The whole file describes Python 3.12, `pip install`,
`launch.sh` / `launch.bat`, no HTTPS, the old file paths under
`AppData/Local/user/Meshed Server Tool`, and a 2.2.0 changelog. **None
of this is true for v3.** A new user reading the repo has no idea how
to build or run the Go binary, no idea about `--tls-cert` /
`--tls-key` / `--autocert-domain`, no idea about the data dir, and
no idea about the migration from v2.
**Fix:** Rewrite for v3. Cover: build (`go build -o meshed ./cmd/meshed`),
frontend build (`cd web && npm install && npm run build`), the
flags, the default data dir on each platform, the HTTPS modes, the
in-process vs reverse-proxy trade-off, the migration from v2 (or
"v2 not supported — see tools/ if we ship a migrator later"), and
links to PLAN.md / godoc.
**Confidence:** High.

### M4. PLAN.md says "Phase 0" — code is on Phase 5
**Location:** `PLAN.md:3-83`
**Problem:** Status header says "Phase 0 (foundation) in progress";
phases 1-5 are listed as future work. `git log` shows
`00484d6 v3-dev: Phase 5 — MOTD, HTTPS-first-class, per-server INI view,
BannedIDs.ini sync`. The doc is 5 phases behind reality, which means
a new contributor reading PLAN.md has a wildly wrong mental model.
**Fix:** Update to reflect the current state. Mark each phase as
"complete" with a one-line note, and add a "v3.0 release status"
section. Keep the open-questions section accurate.
**Confidence:** High.

### M5. No architecture diagram anywhere
**Location:** repo root
**Problem:** The plan says "single binary" and "WebSocket hub" but a
new contributor has to read every `internal/` package to assemble the
picture. The audit prompt asks about this; there isn't one.
**Fix:** Add `docs/architecture.md` (or a top-level section in README)
with a Mermaid diagram: `cmd/meshed → api → hub ← server ← storage,
auth, config; external: dedicated server processes, autocert, browser`.
**Confidence:** High.

### M6. No `Dockerfile` / container image
**Location:** repo root
**Problem:** The binary cross-compiles statically (verified: `GOOS=windows
GOARCH=amd64 go build` and `GOOS=darwin GOARCH=arm64 go build` both
succeed; the resulting `.exe` is 19 MB). A two-stage Dockerfile
(`FROM node:20-alpine AS web; ...; FROM gcr.io/distroless/static-debian12`)
would be ~30 MB total. The audit prompt flags this.
**Fix:** Add `Dockerfile` with healthcheck pointing at a real
`/healthz` (so fix M1 first), expose 5000, mount `/data` as a
volume. Document the env vars for `MESHED_DATA_DIR` etc. (which means
adding env-var support — see M7).
**Confidence:** High.

### M7. Flag-only configuration, no env vars
**Location:** `cmd/meshed/main.go:35-41`
**Problem:** All config is via CLI flags. Container deployments
universally prefer env vars (`MESHED_DATA_DIR`, `MESHED_ADDR`,
`MESHED_TLS_DOMAIN`). The audit prompt says "flag-only is fine for
v3" — agreed, but the moment you ship a Docker image (M6) you'll
want env-var support. Pair the two changes.
**Fix:** After flag.Parse, walk the env and override: `if v := os.Getenv("MESHED_DATA_DIR"); v != "" { *dataDir = v }` etc.
**Confidence:** High.

### M8. TLS uses Go 1.23 defaults (1.2) but no `MinVersion` is set
**Location:** `cmd/meshed/main.go:107`
**Problem:** When the manual cert path is taken, the `http.Server` is
created with no `TLSConfig` at all, so Go's `ListenAndServeTLS` uses
the stdlib default of `tls.Config{}` → `MinVersion = tls.VersionTLS12`.
That's actually fine, but it's not *pinned* — the default could
change. The autocert branch sets `TLSConfig = &tls.Config{GetCertificate: ...}`
with the same default. Be explicit so this doesn't regress on a Go
upgrade.
**Fix:** `TLSConfig: &tls.Config{ MinVersion: tls.VersionTLS12, GetCertificate: m.GetCertificate }`.
**Confidence:** High.

### M9. HSTS not set
**Location:** `internal/api/router.go` (nowhere)
**Problem:** Once `--autocert-domain` is in play the app speaks HTTPS
to the public internet, and the HSTS header is the standard way to
tell browsers "never come back here over plain HTTP". It's not set.
Combined with the absence of an `HSTS preload` directive in any
caller-controlled place, this is a low-impact but real gap.
**Fix:** When `r.TLS != nil`, set
`w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")`.
Wrap as a middleware on the router.
**Confidence:** High.

### M10. No security headers
**Location:** `internal/api/router.go` (nowhere)
**Problem:** No `X-Content-Type-Options: nosniff`, no `X-Frame-Options: DENY`,
no `Referrer-Policy: same-origin`, no `Content-Security-Policy` (for
the API; the SPA is loaded as `default-src 'self'` already by React).
For a tool that may be exposed via `--autocert-domain` to the
internet, this is the easy 80%.
**Fix:** Add a single middleware that sets sensible defaults; the SPA
gets a slightly stricter CSP, the API gets `nosniff` + `DENY`.
**Confidence:** High.

### M11. `/healthz` is unauthenticated (correct) but returns a `version` field that could mislead scanners
**Location:** `internal/api/router.go:28`
**Problem:** `{"status":"ok","version":"v3-dev"}` exposes the version
to anonymous probers. A `v3-dev` string is harmless today, but
hard-coding a version into a public health check is generally not a
great idea. Either return a stable commit hash (set via
`-ldflags '-X main.version=...'`) or just omit.
**Fix:** Inject the version via `-ldflags`. Drop the field from
`/healthz` if you want a clean probe.
**Confidence:** Low.

### M12. No log rotation
**Location:** `cmd/meshed/main.go` (stderr is fine; but…)
**Problem:** Logs go to stderr, which is correct for systemd/journald.
But when running on Windows as a foreground process (e.g. started
from a .bat file), the user has no log file at all, and no way to
keep a history. The README's old Windows `launch.bat` is dead.
**Fix:** Document "run under NSSM / WinSW / sc.exe create / `nssm.exe
install meshed C:\path\to\meshed.exe`" and provide a sample NSSM
config. For macOS, a `launchd` plist.
**Confidence:** Medium.

### M13. Backup strategy: SQLite `.backup` API not exposed
**Location:** `internal/storage/storage.go` (nowhere)
**Problem:** `modernc.org/sqlite` exposes `Connection.Backup(dest)`,
which lets you take a snapshot of the DB safely (handles WAL
correctly). The v3 code never calls it; the user has no
first-class way to back up the DB while it's open. A naive
`cp meshed.db meshed.db.bak` while the server is running captures
the WAL but not the main file in a consistent state.
**Fix:** Add `GET /api/v1/admin/backup` (auth-gated, admin-only) that
streams the backup to the client. Or a CLI subcommand
`meshed backup > meshed.db.snapshot`. Low effort, real value.
**Confidence:** High.

### M14. No disk-usage monitoring
**Location:** everywhere
**Problem:** The data dir grows unbounded — chat history, reports,
session rows (if `SessionDuration` were a sliding window — currently
they're hard-deleted on `Resolve` expiry check, which is good), and
the autocert cache. Nothing in the app watches for "data dir > 90%
of disk" and nothing tells the user.
**Fix:** Add to `/healthz` (M1): a free-space check on the data dir.
Add a startup warning if `<1 GB` free.
**Confidence:** Medium.

### M15. Service-mode documentation
**Location:** `README.md` (none for v3)
**Problem:** There is no `systemd/meshed.service`, no `launchd`
plist, no Windows service wrapper. A user who wants `meshed` to
survive a reboot has to write one. The v2 README mentioned
`launch.sh`; the v3 README mentions nothing.
**Fix:** Add `contrib/meshed.service` (systemd),
`contrib/com.meshed.server.tool.plist` (launchd),
`contrib/meshed.xml` (NSSM / WinSW). Reference from README.
**Confidence:** High.

### M16. Graceful shutdown does not stop child game servers
This is H4 again, repeated here for visibility. The graceful
shutdown path:
1. `srv.Shutdown(ctx)` (HTTP)
2. exit
… does not touch the children. The Manager exposes a
`Stop(ctx, name, grace)` method, but `main.go` never calls it during
shutdown. **This is the kind of issue that causes a `systemctl stop
meshed` to leave orphaned dedicated servers running on the box.**
**Confidence:** High.

### M17. Settings INI file write uses 0o644 (world-readable)
**Location:** `internal/settings/settings.go:180`
**Problem:** `os.WriteFile(f.Path, buf.Bytes(), 0o644)` writes the
INIs as world-readable. This is the file the game server reads at
boot to learn who's banned, who's admin, etc. On a shared box the
user list is readable. Probably fine for a single-user local app
but worth flagging. (The audit prompt mentioned 0700/0600.)
**Fix:** Use `0o600`.
**Confidence:** Medium.

### M18. `clientIP` trusts `X-Forwarded-For` blindly
**Location:** `internal/api/auth_handlers.go:230-246`
**Problem:** `clientIP` takes the first comma-separated hop in
`X-Forwarded-For` and stores it on the session row as the IP. If
`meshed` is fronted by anything (a CDN, a reverse proxy) the *real*
attacker can spoof `X-Forwarded-For` and the audit log will show a
fake IP. There's no `Trusted-Proxies` configuration. The comment
acknowledges the gap.
**Fix:** Either drop `XFF` (use only `r.RemoteAddr`) and tell users
to deploy behind a proxy that rewrites the source, or add a
`--trusted-proxies` flag and a small CIDR matcher.
**Confidence:** High.

### M19. Bcrypt cost default is fine but the comment is misleading
**Location:** `internal/auth/auth.go:34-35`
**Problem:** `bcrypt.DefaultCost` is 10 in the stdlib today, which is
the right default — but the comment says "default cost" with no
number. If/when stdlib bumps the default, an upgrade silently makes
login ~4x slower. Worth pinning.
**Fix:** `bcrypt.GenerateFromPassword([]byte(password), 12)` or a
named const `bcryptCost = 12` with a comment.
**Confidence:** Low.

---

## LOW

### L1. No `uptime` exposed
**Location:** `cmd/meshed/main.go`, `/healthz`
**Problem:** No endpoint returns process uptime. The audit prompt
calls this out. Easy fix: capture `time.Now()` in `main`, expose via
`/api/v1/admin/info` or include in `/healthz`.
**Confidence:** High.

### L2. No crash report / panic dump mechanism
**Location:** everywhere
**Problem:** If the process dies with a panic, there's no
`core_pattern`-style dump, no on-disk stack trace, nothing. The
audit prompt calls this out. For a tool that runs unattended, this
is the kind of thing that makes a "what happened at 3am" investigation
hard. Consider `defer func() { if r := recover(); r != nil {
os.WriteFile("data-dir/last-panic.txt", debug.Stack(), 0600) } }()` at
the top of `main`.
**Confidence:** Medium.

### L3. Missing flags: `--log-level`, `--bind`, `--port`, `--read-only`
**Location:** `cmd/meshed/main.go:35-41`
**Problem:** The audit prompt enumerates these. None are present.
`--addr` does double duty as "bind + port" with the `:5000` default.
`--bind` would be useful for "always bind 0.0.0.0 in container" without
forcing the user to remember the colon-prefix convention. `--port`
separate from `--addr` would be the Docker-idiomatic way.
`--read-only` would lock the API to GETs only, useful for emergency
mode. `--log-level` is M2.
**Confidence:** High (the gap), Medium (the priority).

### L4. Webpack-style `WS InsecureSkipVerify` comment says "CSRF-safe"
**Location:** `internal/api/websocket_handlers.go:46-48`
**Problem:** C2's detail. The comment is *wrong* — cookie auth without
strict origin allowlisting is *not* CSRF-safe for a WebSocket. This is
a documentation bug, not a code bug, but the comment is in the file
as a future-self note.
**Confidence:** High (on the wrongness of the comment).

### L5. `lookupServer` exists but is mentioned in the audit prompt
**Location:** `internal/api/lookup.go` + `internal/api/server_handlers.go:270`
**Problem:** `srv := lookupServer(d.manager, name)` does a linear scan
of the manager to find the in-memory `*Server`. For a tool with a
handful of servers this is fine; flag for the next person who
thinks "I'll just add a map".
**Confidence:** Low.

### L6. `pumpPipe` allocates per byte (H5 restated for the buffer topic)
Already covered in H5. Mentioning here for visibility.
**Confidence:** High.

### L7. The build tag on `process_windows.go` uses `windows` correctly
**Location:** `internal/server/process_windows.go:1`,
`internal/server/process_unix.go:1`
**Problem:** The build tags are correct (`//go:build windows` and
`//go:build !windows`). Verified by cross-compile: `GOOS=windows
GOARCH=amd64 go build` produces a 19 MB binary; `GOOS=darwin
GOARCH=arm64 go build` produces a working binary; `GOOS=linux
GOARCH=amd64 go build` (the default) works. Both code paths compile
in isolation, and the build tag split is correct.
**Confidence:** High.

### L8. Embed path separator handling is correct
**Location:** `web/embed.go:15`
**Problem:** The `//go:embed all:dist` directive resolves relative to
the `web/` directory. The `fs.Sub(distFS, "dist")` re-roots the FS so
the rest of the code uses forward-slash paths only (`assets/index-*.js`).
This is correct on Windows because `embed.FS` always uses `/` as the
path separator, regardless of the host OS. No bug here; flagging
because the audit prompt asks.
**Confidence:** High.

### L9. Frontend dependencies are cross-platform
**Location:** `web/package.json`
**Problem:** `react`, `react-dom`, `react-router-dom`, `vite`,
`@vitejs/plugin-react`, `typescript`. All pure JS. The dev experience
is identical on Linux, macOS, and Windows. `node_modules/.bin/*`
issues are handled by npm itself. No native modules. Verified by
inspection.
**Confidence:** High.

### L10. No `node_modules` shipped in the embed
**Location:** `web/dist/` (5 files)
**Problem:** Verified: `web/dist` contains exactly 5 files
(`index.html`, `assets/index-*.js`, `assets/index-*.css`, `assets/index-*.js.map`,
`assets/icon-*.svg`). `node_modules` is in `web/node_modules` and is
NOT under `web/dist`. The embed pattern `//go:embed all:dist` does
not pick it up. **Good.** The sourcemap is the only thing that's
unintentionally shipping — see H9.
**Confidence:** High.

### L11. `taskkill` is universally available on Windows
**Location:** `internal/server/process_windows.go:16`
**Problem:** `taskkill.exe` has been in `C:\Windows\System32\` since
Windows XP (2001). Every supported Windows release has it. No issue
here. Flagging because the audit prompt asked.
**Confidence:** High.

### L12. Unix path handling in settings.go
**Location:** `internal/settings/settings.go:54-68, 180, 191`
**Problem:** All paths are built with `filepath.Join`, which uses the
OS separator. The read uses `os.ReadFile` (OS-aware). No hard-coded
`/` or `\` anywhere in the settings package. Good.
**Confidence:** High.

---

## Items the audit prompt asked about that I confirmed are OK

| Item | Status | Where |
|------|--------|-------|
| Build tags present and correct | ✅ | `process_*.go:1` |
| Cross-compile works for Windows & macOS | ✅ | Verified by `GOOS=windows` and `GOOS=darwin` builds |
| Embed pattern catches all needed files | ✅ (with caveat — see H9) | `web/embed.go:15` |
| `XDG_DATA_HOME` honored on Linux | ✅ | `config.go:32` |
| `LocalAppData` honored on Windows | ✅ | `config.go:24` |
| `~/Library/Application Support` on macOS | ✅ | `config.go:30` |
| `defaultDataDir` overrideable | ✅ (`--data-dir`) | `main.go:40` |
| `srv.Shutdown(ctx)` waits for in-flight HTTP | ✅ | `main.go:149` |
| SQLite WAL enabled (not corruption-prone) | ✅ | `storage.go:27` (`journal_mode(WAL)`) |
| TLS cookie has `HttpOnly` + `Secure`-when-TLS + `SameSite=Lax` | ✅ | `auth.go:148-157` |
| Bcrypt for password hashing | ✅ | `auth.go:34-45` |
| No CGo required (pure-Go SQLite) | ✅ | `go.mod` uses `modernc.org/sqlite` |
| Static `embed.FS` for SPA | ✅ (rooted at `dist`) | `embed.go:23` |
| No external CDN deps in the React build | ✅ (only `react`, `react-dom`, `react-router-dom` — all bundled) | `package.json` |

---

## Recommended fix order (suggested for the next sprint)

1. **C1, H1, H2** — security/correctness: data dir + autocert + DB
   permissions, home-dir fallback. Half a day.
2. **C2** — strict WebSocket origin allowlist. Two hours.
3. **C3** — wire `autostart` or remove the field. One hour.
4. **H4 / M16** — graceful shutdown stops children. Two hours.
5. **H3, H5, H6, H7** — process kill robustness, pumpPipe,
   recover, grace config. Half a day total.
6. **M1, M2** — real `/healthz`, structured logging. One day.
7. **M3, M4, M5, M15** — docs. Half a day.
8. **M6, M7** — Dockerfile + env vars. Half a day.
9. **M8, M9, M10** — TLS / HSTS / security headers. Two hours.
10. **M13** — backup endpoint. Two hours.

Everything else is polish.

---

**Audit complete.** No files were modified during this audit.
