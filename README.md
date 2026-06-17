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
- Optional HTTPS with autocert
- Cookie-based auth, bcrypt-hashed passwords, automatic first-user bootstrap

## Quick start

1. Download `meshed` (or build from source — see below).
2. Run it: `./meshed -addr :8080 -data-dir ./data`
3. Open <http://localhost:8080>.
4. Create the first user — bootstrap is automatic on first visit.

## Build from source

Requires Go 1.23+ and Node 22+.

```sh
go build -o meshed ./cmd/meshed
(cd web && npm install && npm run build)
```

The frontend is embedded into the binary at build time, so you only ship `meshed` (plus its `-data-dir`).

## Configuration

| Flag           | Default            | Notes                                                                                  |
|----------------|--------------------|----------------------------------------------------------------------------------------|
| `-addr`        | `:8080`            | Listen address                                                                         |
| `-data-dir`    | platform-specific  | SQLite DB + per-server state (see below)                                               |
| `-tls-cert`    | unset              | Path to TLS certificate                                                                |
| `-tls-key`     | unset              | Path to TLS private key                                                                |
| `-autocert`    | `false`            | Use Let's Encrypt (requires `-addr :443` and a reachable hostname)                     |

Without `-tls-cert` / `-tls-key` (and without `-autocert`) the binary listens on plain HTTP.

Default data directories:

- Linux: `~/.local/share/meshed-server-tool/`
- macOS: `~/Library/Application Support/meshed-server-tool/`
- Windows: `%LocalAppData%\meshed-server-tool\`

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

## License

MIT — see [LICENSE](LICENSE).

## v2 (legacy)

The Python/Flask v2 source is preserved on the `main` branch for historical reference. It is no longer maintained and is not built or referenced from this branch.
