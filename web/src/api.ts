// Public API surface for talking to the Meshed backend.
//
// The auth API uses cookie-based sessions; the browser sends cookies
// automatically when `credentials: "include"` is set on fetch.

export type Role = "admin" | "user";

export interface AuthStatus {
  bootstrap_available: boolean;
  authenticated: boolean;
  username?: string;
  role?: Role;
}

export interface MeResponse {
  username: string;
  role: Role;
}

const API = ""; // same-origin; Vite proxy in dev, embedded static in prod

async function request<T>(
  path: string,
  init: RequestInit = {},
): Promise<T> {
  const res = await fetch(API + path, {
    ...init,
    credentials: "include",
    headers: {
      "Content-Type": "application/json",
      ...(init.headers ?? {}),
    },
  });
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const body = (await res.json()) as { error?: string };
      if (body?.error) msg = body.error;
    } catch {
      // body wasn't JSON; keep the status text
    }
    throw new Error(msg);
  }
  return (await res.json()) as T;
}

export const api = {
  status: () => request<AuthStatus>("/api/v1/auth/status"),
  me: () => request<MeResponse>("/api/v1/auth/me"),
  login: (username: string, password: string) =>
    request<MeResponse>("/api/v1/auth/login", {
      method: "POST",
      body: JSON.stringify({ username, password }),
    }),
  logout: () => request<{ ok: boolean }>("/api/v1/auth/logout", { method: "POST" }),
  bootstrap: (username: string, password: string) =>
    request<MeResponse>("/api/v1/auth/bootstrap", {
      method: "POST",
      body: JSON.stringify({ username, password }),
    }),
};
