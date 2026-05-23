# MeshedServerTool V2 behavior inventory

This document captures the current working responsibilities of MeshedServerTool V2 so a V3 rewrite can preserve operational behavior before expanding into new modules or game integrations.

## Current product role

MeshedServerTool V2 is a locally hosted web interface and server manager for SCP: 5K / SCP Pandemic dedicated servers.

The current README documents these working product responsibilities:

- create and configure servers through a web interface;
- log relevant details for all servers through one interface;
- manage servers through the web interface;
- view in-game reports and perform easy/global banning across servers.

## Current storage model

The README states MeshedServerTool stores settings under platform-specific user data/config locations, including:

- `C:\Users\User\AppData\Local\user\Meshed Server Tool`
- `/home/user/.local/share/Meshed Server Tool`
- `/home/user/.config/Meshed Server Tool`

V3 should document and import the existing storage layout rather than inventing a new layout without a compatibility path.

## Current web surface observed in `MeshedWebServer.py`

### Auth and pages

- `/login`
- `/create-user`
- `/logout`
- `/`
- `/reports`
- `/server/<server_name>`
- `/create-server`
- `/steamcmd-guide`
- `/logs/<page>`
- `/chat/<page>`

### Live streams

- `/stream-server-info`
- `/stream-server-info-encoded`
- `/stream-all-server-logs`
- `/stream-all-chat-logs`
- `/server/<server_name>/stream-server-logs`
- `/server/<server_name>/stream-chat-logs`
- `/stream-new-reports-quantity`
- `/stream-new-reports`

### Server control and settings endpoints

- `PUT /control-server`
- `GET/PUT /server/<server_name>/management-settings`
- `GET/PUT /server/<server_name>/map-settings`
- `GET/PUT /server/<server_name>/players-settings`
- `GET/PUT /server/<server_name>/server-settings`
- `GET/PUT /server/<server_name>/gameplay-settings`
- `POST /servers`

### Reports

- `POST /reports` — ban from report
- `DELETE /reports` — delete report
- `PUT /reports` — mark report read/handled

## Current backend responsibilities observed in code

V2 already contains operational behavior that V3 should preserve or explicitly replace:

- server config path resolution;
- management/map/player/server/gameplay config read and write;
- server list persistence;
- report persistence and per-user report grouping;
- log paging;
- chat log paging;
- server object creation and initialization;
- shared install handling;
- server launch command construction;
- start/restart/stop/kill lifecycle commands;
- server suspend/idle/crash handling;
- log analysis;
- game start detection;
- chat message parsing;
- status transitions;
- global banlist update/materialization;
- all-server initialization;
- report-checking thread initialization;
- web server initialization.

## Compatibility implication

V3 should not treat MeshedServerTool as a greenfield Arma AI platform. V3 is first a replacement for this working SCP:5K/Pandemic server manager. New module architecture should be added only after the existing server-manager responsibilities are represented in the V3 model, fixtures, and tests.
