# MeshedServerTool v3

Web-based manager for **SCP: 5k** and **SCP Pandemic** dedicated servers. A single static Go binary ships the API, the React UI, and an embedded SQLite database — no Python, no Node runtime, no CGo on the host.

## Features

- Server lifecycle: create / start / stop / restart / delete
- Real-time log tailing over **WebSockets** (with REST `?tail=N` snapshots for history)
- Reports queue + global ban list
- Per-server chat history + cross-server `/chat` view
- MOTD (global and per-server)
- INI settings editor — `BannedIDs.ini` plus any `.ini` in the install dir
- Per-server settings tabs: **Map**, **Players**, **Server**, **Gameplay**
- Cross-server `/logs` aggregate view
- SteamCMD install guide built in
- Optional HTTPS with autocert (Let's Encrypt) or manual certs
- Cookie-based auth, bcrypt-hashed passwords, automatic first-user bootstrap
- Single binary, no runtime dependencies

## Quick start

1. Download `meshed` for your platform (see [Releases](https://github.com/Skomesh/MeshedServerTool/releases)), or build from source — see below.
2. Run it: `./meshed -addr :5000`
3. Open <http://localhost:5000>.
4. Create the first user — bootstrap is automatic on first visit.

## Build from source

Requires Go 1.25+ and Node 22+.

```sh
go build -o meshed ./cmd/meshed
(cd web && npm install && npm run build)
```

The frontend is embedded into the binary at build time, so you only ship `meshed` (plus its `-data-dir`).

To stamp a version into `/healthz`:

```sh
go build -ldflags '-X main.version=v3.0.0' -o meshed ./cmd/meshed
```

## Configuration

All flags:

- `-addr` — listen address (default `:5000`; e.g. `127.0.0.1:8080` to bind localhost only)
- `-data-dir` — override data directory (default: platform-specific user data dir)
- `-tls-cert` / `-tls-key` — path to TLS certificate + private key (enables HTTPS)
- `-autocert-domain` — domain for Let's Encrypt (e.g. `mesh.example.com`; requires port 80 reachable for HTTP-01)
- `-autocert-cache` — directory for autocert cert cache (default: `<data-dir>/autocert`)
- `-trusted-proxies` — comma-separated CIDR list whose `X-Forwarded-For` is honored (default: empty — never trust XFF, prevents session IP spoofing)
- `-log-level` — `debug` | `info` | `warn` | `error` (default `info`)

Default data directories:

- Linux: `~/.local/share/meshed-server-tool/` (or `$XDG_DATA_HOME/meshed-server-tool`)
- macOS: `~/Library/Application Support/Meshed Server Tool`
- Windows: `%LocalAppData%\Skomesh\Meshed Server Tool`

The data directory contains the SQLite database, per-server state, and the autocert cache. It is created with mode `0700`; the database file is `0600`.

## Running as a service

Sample service descriptors for all three major platforms live in [`contrib/`](contrib/):

- **Linux (systemd):** [`contrib/meshed.service`](contrib/meshed.service) — drops in to `/etc/systemd/system/`. Includes systemd hardening (ProtectSystem, NoNewPrivileges, etc.) and a 30s graceful-shutdown timeout.
- **macOS (launchd):** [`contrib/meshed.plist`](contrib/meshed.plist) — drops in to `/Library/LaunchDaemons/`. Logs to `/var/log/meshed.{out,err}`.
- **Windows (WinSW):** [`contrib/meshed.xml`](contrib/meshed.xml) — pair with `winsw.exe` renamed to `meshed.exe`. Registers as a real Windows service via `meshed.exe install`.

Each file has install instructions in the header comments.

## Backup and restore

`GET /api/v1/admin/backup` (admin role required) streams a consistent SQLite snapshot via `VACUUM INTO`. The response is a self-contained `.db` file. To restore: stop `meshed`, copy the file to `<data-dir>/meshed.db` (replacing the live DB), and start `meshed` again. Schema migrations run automatically on first open.

## Architecture

- `cmd/meshed/` — entry point, flag parsing, graceful shutdown
- `internal/api/` — HTTP handlers (REST + WebSocket)
- `internal/auth/` — bcrypt + cookie sessions + first-user bootstrap
- `internal/storage/` — SQLite via `modernc.org/sqlite` (pure Go, no CGo)
- `internal/server/` — process manager, state machine, cross-platform kill
- `internal/logs/` — ring buffer + structured log line parser
- `internal/hub/` — pub/sub fan-out for WebSocket events
- `internal/reports/`, `internal/bans/`, `internal/chat/`, `internal/motd/`, `internal/settings/`, `internal/tabs/`
- `web/` — React 18 + TypeScript + Vite, built into a static bundle and embedded into the Go binary at build time
- `contrib/` — sample service descriptors (systemd, launchd, WinSW)

## Continuous integration

GitHub Actions on every push to `main` / `v3-dev`:

- **Go workflow** — `go test -race -count=1`, `go vet`, `gofmt` gate, cross-compile smoke for Windows / macOS / Linux
- **Web workflow** — `npm ci && npm run build`, verifies the embed has no sourcemap files and the React root mount is present

## License

MIT — see [LICENSE](LICENSE).

## v2 (legacy)

The Python/Flask v2 source is preserved on the `main` branch for historical reference. It is no longer maintained and is not built or referenced from this branch.
