# MeshedServerTool V3 foundation plan

## Locked project statement

MeshedServerTool is the foundation. V3 is a rewrite and expansion of the existing working MeshedServerTool server manager. SCP:5K parity is the first production target. A3AINPC is a managed Arma AI module that V3 can supervise later.

All AI/TTS work should run server-side. Generated voice requires a client playback transport. Text-only Arma is the first public-compatible Arma mode. Private voiced Arma comes only after engine, BattlEye, and audio-transport gates pass.

## Product hierarchy

| Layer | Role |
| --- | --- |
| MeshedServerTool V2 | Proven working SCP:5K/Pandemic server manager and operational foundation. |
| MeshedServerTool V3 | Modular rewrite that preserves SCP:5K parity first, then adds profile/module architecture. |
| SCP:5K profile | First production profile and V3 MVP target. |
| Managed services | Generic service supervision for companion processes such as LLM/TTS/audio workers. |
| A3AINPC | Optional later Arma AI managed module, not the root product. |

## Foundation rule

Do not design V3 as a greenfield Arma AI app. Design V3 as the next generation of MeshedServerTool, then plug A3AINPC into it as one managed module.

## V3 core responsibilities

V3 should generalize the current server-manager behavior into reusable control-plane components:

- create/manage dedicated servers;
- edit game/server/profile config;
- start/restart/stop/kill server processes;
- supervise process health;
- tail logs;
- parse game events;
- expose live dashboard state;
- handle reports;
- write/materialize global bans;
- manage per-server files;
- support shared install directories;
- provide authenticated web UI.

## Suggested architecture

```text
backend/
  app.ts
  server.ts
  api/
    auth.routes.ts
    servers.routes.ts
    services.routes.ts
    reports.routes.ts
    bans.routes.ts
    logs.routes.ts
  services/
    ProcessManager
    ServiceManager
    ConfigMaterializer
    LogTailer
    EventBus
    ReportScanner
    BanlistWriter
    GameProfileRegistry
    ManagedServiceRegistry
  profiles/
    scp5k/
    arma3/
  modules/
    arma-ai/
  prisma/
frontend/
  dashboard
  server detail
  service detail
  logs
  reports
  settings
```

## API rule

Use REST for mutation/control commands. Use Socket.IO for state, log, and event streams. Do not make Socket.IO the primary mutation API.

## Foundation profile: SCP:5K

The SCP:5K profile should own game-specific behavior:

- launch command builder;
- shared install path resolver;
- INI materializer;
- map rotation serializer;
- gameplay config parser;
- regex log parser;
- report scanner;
- banlist writer.

## Optional module: A3AINPC

After SCP:5K parity is stable, A3AINPC can be supervised by Meshed V3 through `ManagedServiceProfile` definitions:

- Arma 3 dedicated server service profile;
- A3AINPC middleware service profile;
- llama-server service profile;
- Kokoro/Piper TTS service profile;
- audio server service profile;
- extension build/artifact status;
- dialogue job contract;
- NPC profile contract;
- signed audio metadata;
- audio transport abstraction.

## Database model direction

Core server rows should stay generic. SCP-specific settings belong in profile config namespaces.

```prisma
model ManagedServer {
  id Int @id @default(autoincrement())
  kind String // scp5k | arma3 | future
  name String @unique
  displayName String?
  installDir String?
  workingDir String?
  executablePath String?
  argsJson Json?
  envJson Json?
  status String @default("Offline")
  enabled Boolean @default(true)
  createdAt DateTime @default(now())
  updatedAt DateTime @updatedAt
  configs ServerConfig[]
  services ManagedService[]
  events RuntimeEvent[]
}

model ManagedService {
  id Int @id @default(autoincrement())
  serverId Int?
  kind String // llama-server | kokoro | a3ainpc | audio-server
  name String
  command String
  argsJson Json?
  envJson Json?
  healthUrl String?
  bindAddress String?
  port Int?
  status String @default("Offline")
  createdAt DateTime @default(now())
  updatedAt DateTime @updatedAt
}

model ServerConfig {
  id Int @id @default(autoincrement())
  serverId Int
  namespace String // scp5k.management | scp5k.map | arma-ai.dialogue
  data Json
  materializedTo String?
  createdAt DateTime @default(now())
  updatedAt DateTime @updatedAt
}

model RuntimeEvent {
  id Int @id @default(autoincrement())
  serverId Int?
  serviceId Int?
  type String
  payload Json
  createdAt DateTime @default(now())
}
```

A3AINPC-specific tables should be added only when the managed module integration begins.
