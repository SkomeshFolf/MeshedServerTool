# MeshedServerTool V3 roadmap

## Phase 0 — V2 compatibility map

Before writing V3 code, document current working behavior from V2.

Deliverables:

- `docs/current-v2-behavior.md`
- `docs/v2-to-v3-compatibility.md`
- `fixtures/scp5k/` config, log, and report fixtures

Capture:

- where V2 stores app data;
- how `Server_*` folders are discovered;
- how shared install directories work;
- how process lifecycle works;
- how logs are tailed;
- how reports are parsed;
- how global bans are written;
- how web controls map to backend functions.

## Phase 1 — V3 core control plane

Build the generic server/service manager:

- `ManagedServer`
- `ManagedService`
- `GameProfile`
- `ManagedServiceProfile`
- `ProcessManager`
- `ConfigMaterializer`
- `LogTailer`
- `RuntimeEvent`
- REST API
- Socket.IO state/log streams
- server-side session auth
- PostgreSQL/Prisma
- React dashboard shell

## Phase 2 — SCP:5K parity

Port existing Meshed behavior first:

- legacy config import;
- create server;
- edit server settings;
- edit map settings;
- edit gameplay settings;
- edit admins/owners/whitelist;
- start/restart/stop/kill server;
- stop after game;
- crash/restart behavior;
- active-hours behavior;
- live dashboard;
- live logs;
- live chat logs;
- reports;
- mark report handled;
- ban from report;
- global banlist materialization.

This is the real V3 MVP. A3AINPC must not block it.

## Phase 3 — test harness

Add tests around working server-manager behavior:

- SCP:5K config import tests;
- INI roundtrip tests;
- map rotation tests;
- process manager dummy server tests;
- log tailer tests;
- report scanner tests;
- banlist writer tests;
- auth tests;
- REST permission tests;
- Socket.IO subscription tests;
- filesystem safety tests.

## Phase 4 — A3AINPC as managed service module

Only after SCP:5K parity is stable, connect A3AINPC through `ManagedServiceProfile`.

Managed services:

- Arma 3 dedicated server;
- A3AINPC middleware;
- llama-server;
- Kokoro worker;
- Piper fallback;
- audio server;
- extension build/artifact status.

## Phase 5 — Arma text-only AI MVP

The first Arma feature managed by Meshed should be text-only:

- start/stop Arma server;
- start/stop A3AINPC middleware;
- health-check llama-server;
- configure NPC profile;
- request dialogue;
- return subtitle via `remoteExec`;
- no client audio extension;
- no generated voice.

## Phase 6 — voiced Arma private mode

Only after engine gates pass:

- signed audio IDs;
- read-only audio endpoint;
- client extension download;
- SHA-256 verification;
- byte-size cap;
- local `playSound3D`;
- subtitle fallback;
- BattlEye status documented.
