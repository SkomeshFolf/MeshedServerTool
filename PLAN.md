# MeshedServerTool v3 — Implementation Plan

> **Status:** Phase 0 (foundation) in progress.
> **Stack:** Go 1.23 backend + React/TypeScript frontend (Vite), single-binary deploy.

## Why this rewrite

v2 (Python/Flask) is feature-complete but its deployment story is painful:
the user must install Python 3.12, a venv, and a dozen pip packages. v3
collapses everything into a single static binary that runs anywhere.

## Architecture

Single repo, two sub-projects:

```
MeshedServerTool/
├── cmd/meshed/        Go entry point
├── internal/          private Go packages
│   ├── config/        paths, defaults
│   ├── auth/          sessions, password hashing
│   ├── server/        subprocess lifecycle for game servers
│   ├── logs/          log tailing, parsing
│   ├── reports/       in-game reports
│   ├── storage/       SQLite layer
│   └── api/           HTTP routes, WebSocket hub
├── web/               React frontend (Vite + TypeScript)
└── PLAN.md            this file
```

**Frontend ↔ backend:** React build outputs to `web/dist/`, which Go embeds
via `go:embed`. The single binary serves the UI and the API.

**Real-time:** WebSockets (`nhooyr.io/websocket`), not polling SSE.

**Storage:** SQLite via `modernc.org/sqlite` (pure-Go, no CGo, cross-compiles).

**TLS:** stdlib `crypto/tls` + Let's Encrypt via `autocert` (optional).

## Phases

### Phase 0 — Foundation ✅ in progress
- Go module initialized, directory layout set
- `cmd/meshed/main.go` with flag parsing, signal handling, graceful shutdown
- `internal/storage` opens SQLite (real driver, minimal schema)
- `internal/api` wires `/healthz`, `/api/v1/`, and embedded static fallback
- Vite + React + TypeScript scaffold
- `web/embed.go` exposes the React build to Go
- One end-to-end smoke test: build → run binary → React dashboard loads

### Phase 1 — Storage + auth
- Real SQLite schema: `users`, `sessions`, `servers`, `reports`, `bans`, `motd`
- `internal/auth` with bcrypt password hashing + session tokens
- `POST /api/v1/auth/login`, `POST /api/v1/auth/logout`
- React login flow

### Phase 2 — Server manager core
- `internal/server` package: `Server` type wrapping `os/exec`
  - Start/stop/restart with proper signal handling per OS
  - Status tracking (stopped, starting, running, crashed)
  - Process info via `gopsutil` (cross-platform `psutil` equivalent)
- `internal/logs` package: `fsnotify` watcher for log files
- API: `GET/POST /api/v1/servers`, `POST /api/v1/servers/{id}/start|stop|restart`
- React: server list + server detail page

### Phase 3 — WebSocket real-time
- `internal/api` WebSocket hub: clients subscribe to per-server or global events
- React: WebSocket client with reconnection, server status, log tail in UI

### Phase 4 — Port v2 features
- Reports: parse, list, mark read, ban action
- Chat logs: same pipeline as server logs
- Server settings: read/write INI files in the server install dir

### Phase 5 — v3-only features (the actual reason for v3)
- **Server deletion** (v2 cannot do this)
- **Global ban list** (v2 mentions but doesn't ship)
- **MOTD system** (v2 TODO)
- **HTTPS as first-class** — `meshed --tls-cert --tls-key` already works; add
  self-signed dev cert generator and a `meshed --tls=auto` mode using `autocert`
- **Settings for logging speed** (v2 TODO)
- **Log history + raw log view** (v2 only tails)

## Build & run

```bash
# Backend
go build -o meshed ./cmd/meshed
./meshed                       # serves on :5000

# Frontend dev
cd web && npm install && npm run dev   # dev server on :5173, proxies API to :5000

# Production build
cd web && npm run build       # produces web/dist/
go build -o meshed ./cmd/meshed   # embeds web/dist → single binary
```

## Open questions for the user

- Should v3 preserve v2's on-disk layout (config files per server in appdata)
  or normalize everything to SQLite? (Recommend: settings in SQLite, server
  install dir untouched, log history optionally indexed in SQLite.)
- Migration path for v2 users: read their `users.json` + per-server
  configs on first run, or clean break? (Recommend: clean break for v3.0,
  ship a v2-to-v3 migration tool later if asked.)
