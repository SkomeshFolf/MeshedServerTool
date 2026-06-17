// Extended API surface for server management.
//
// Re-uses the request helper from ./api.ts (which handles cookies and
// JSON encoding), but the types live here so server pages don't have
// to import auth types.

import { request } from "./api";

export type ServerStatus =
  | "stopped"
  | "starting"
  | "running"
  | "stopping"
  | "crashed";

export interface ServerConfig {
  name: string;
  install_dir: string;
  port: number;
  max_players: number;
  hostname: string;
  args: Record<string, unknown>;
  autostart: boolean;
  created_at: string;
  updated_at: string;
}

export interface ServerState {
  server_name: string;
  status: ServerStatus;
  pid?: number;
  started_at?: string;
  stopped_at?: string;
  last_exit_code?: number;
  current_users: number;
  current_map: string;
  current_gamemode: string;
  updated_at: string;
}

export interface ServerView {
  name: string;
  install_dir: string;
  port: number;
  max_players: number;
  hostname: string;
  args: Record<string, unknown>;
  autostart: boolean;
  created_at: string;
  updated_at: string;
  state: ServerState;
}

export interface LogLine {
  at: string;
  stream: "stdout" | "stderr";
  text: string;
  type?: string;
  fields?: Record<string, unknown>;
}

export const serversApi = {
  list: () =>
    request<{ servers: ServerView[] }>("/api/v1/servers").then((r: { servers: ServerView[] }) => r.servers),
  get: (name: string) => request<ServerView>(`/api/v1/servers/${encodeURIComponent(name)}`),
  create: (body: {
    name: string;
    install_dir: string;
    port?: number;
    max_players?: number;
    hostname?: string;
    args?: Record<string, unknown>;
    autostart?: boolean;
  }) => request<ServerView>("/api/v1/servers", { method: "POST", body: JSON.stringify(body) }),
  update: (name: string, body: Partial<ServerConfig>) =>
    request<ServerView>(`/api/v1/servers/${encodeURIComponent(name)}`, {
      method: "PATCH",
      body: JSON.stringify(body),
    }),
  delete: (name: string) =>
    request<{ ok: boolean }>(`/api/v1/servers/${encodeURIComponent(name)}`, {
      method: "DELETE",
    }),
  start: (name: string) =>
    request<ServerView>(`/api/v1/servers/${encodeURIComponent(name)}/start`, { method: "POST" }),
  stop: (name: string) =>
    request<ServerView>(`/api/v1/servers/${encodeURIComponent(name)}/stop`, { method: "POST" }),
  restart: (name: string) =>
    request<ServerView>(`/api/v1/servers/${encodeURIComponent(name)}/restart`, { method: "POST" }),
  logsTail: (name: string, n: number) =>
    request<{ lines: LogLine[] }>(
      `/api/v1/servers/${encodeURIComponent(name)}/logs?tail=${n}`,
    ).then((r: { lines: LogLine[] }) => r.lines),
  // SSE is consumed by the EventSource API directly in the page; the typed
  // `request` helper above doesn't fit long-lived streams.
  logsStreamURL: (name: string) =>
    `/api/v1/servers/${encodeURIComponent(name)}/logs`,
};

// --- Reports ---

export interface Report {
  id: number;
  server_name: string;
  target_id: string;
  target_name: string;
  source_id: string;
  source_name: string;
  date: string;
  reason: string;
  text: string;
  hash: string;
  handled: boolean;
  handled_at?: string;
  created_at: string;
}

export const reportsApi = {
  list: (params?: { handled?: boolean; server?: string; limit?: number }) => {
    const q = new URLSearchParams();
    if (params?.handled !== undefined) q.set("handled", String(params.handled));
    if (params?.server) q.set("server", params.server);
    if (params?.limit) q.set("limit", String(params.limit));
    const qs = q.toString();
    return request<{ reports: Report[]; reports_per_user: Record<string, number> }>(
      `/api/v1/reports${qs ? "?" + qs : ""}`,
    );
  },
  get: (id: number) => request<Report>(`/api/v1/reports/${id}`),
  create: (body: Omit<Report, "id" | "hash" | "handled" | "handled_at" | "created_at">) =>
    request<Report>("/api/v1/reports", { method: "POST", body: JSON.stringify(body) }),
  markHandled: (id: number, handled: boolean) =>
    request<Report>(`/api/v1/reports/${id}`, {
      method: "PATCH",
      body: JSON.stringify({ handled }),
    }),
  delete: (id: number) =>
    request<{ ok: boolean }>(`/api/v1/reports/${id}`, { method: "DELETE" }),
};

// --- Bans ---

export interface Ban {
  id: number;
  steam_id: string;
  player_name: string;
  reason: string;
  banned_by: string;
  banned_at: string;
}

export const bansApi = {
  list: () => request<{ bans: Ban[] }>("/api/v1/bans").then((r: { bans: Ban[] }) => r.bans),
  add: (body: { steam_id: string; player_name?: string; reason?: string }) =>
    request<Ban>("/api/v1/bans", { method: "POST", body: JSON.stringify(body) }),
  remove: (id: number) =>
    request<{ ok: boolean }>(`/api/v1/bans/${id}`, { method: "DELETE" }),
};

// --- Chat ---

export interface ChatMessage {
  id: number;
  server_name: string;
  player_name: string;
  message: string;
  at: string;
}

export const chatApi = {
  list: (serverName: string, limit = 200) =>
    request<{ messages: ChatMessage[] }>(`/api/v1/chat/${encodeURIComponent(serverName)}?limit=${limit}`)
      .then((r: { messages: ChatMessage[] }) => r.messages),
};

// --- MOTD ---

export interface MOTD {
  server_name?: string;
  message: string;
  enabled: boolean;
  updated_at: string;
  updated_by: string;
}

export const motdApi = {
  list: () =>
    request<{ global: MOTD; servers: MOTD[] }>("/api/v1/motd"),
  getForServer: (serverName: string) =>
    request<{ message: MOTD; per_server: boolean }>(`/api/v1/motd/${encodeURIComponent(serverName)}`),
  setForServer: (serverName: string, body: { message: string; enabled?: boolean }) =>
    request<MOTD>(`/api/v1/motd/${encodeURIComponent(serverName)}`, {
      method: "PUT",
      body: JSON.stringify(body),
    }),
  deleteForServer: (serverName: string) =>
    request<{ ok: boolean }>(`/api/v1/motd/${encodeURIComponent(serverName)}`, {
      method: "DELETE",
    }),
  setGlobal: (message: string) =>
    request<MOTD>("/api/v1/motd", { method: "PUT", body: JSON.stringify({ message }) }),
};

// --- Settings (per-server INI files) ---

export interface INIEntry { key: string; value: string; }
export interface INISection { name: string; entries: INIEntry[]; }
export interface INIFile {
  path: string;
  rel_path: string;
  sections: INISection[];
}

export const settingsApi = {
  list: (serverName: string) =>
    request<{ files: string[] }>(`/api/v1/servers/${encodeURIComponent(serverName)}/settings`),
  read: (serverName: string, file: string) =>
    request<INIFile>(`/api/v1/servers/${encodeURIComponent(serverName)}/settings/${encodeURIComponent(file)}`),
  write: (serverName: string, file: string, body: INIFile) =>
    request<{ ok: boolean }>(`/api/v1/servers/${encodeURIComponent(serverName)}/settings/${encodeURIComponent(file)}`, {
      method: "PUT",
      body: JSON.stringify(body),
    }),
  syncBans: (serverName: string) =>
    request<{ ok: boolean; ban_count: number; path: string }>(
      `/api/v1/servers/${encodeURIComponent(serverName)}/settings/sync-bans`,
      { method: "POST" },
    ),
};
