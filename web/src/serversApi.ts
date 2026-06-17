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
