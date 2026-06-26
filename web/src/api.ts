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

// Custom DOM event for session expiry. Fired from request() when it
// sees a 401 that wasn't itself the /auth/* call. The AuthProvider
// listens for this and clears state + redirects to /login. Keeps the
// fetch helper free of React Router / context dependencies.
export const SESSION_EXPIRED_EVENT = "meshed:session-expired";
function fireSessionExpired(path: string) {
  window.dispatchEvent(new CustomEvent(SESSION_EXPIRED_EVENT, { detail: { path } }));
}

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
    // Session expiry: any 401 on a non-auth endpoint means the cookie
    // is stale or the user was deleted. Fire the global event so the
    // auth provider can clear state and redirect to /login. The
    // thrown error still surfaces to the caller so the per-call
    // catch (which now produces a toast) doesn't go silent.
    if (res.status === 401 && !path.startsWith("/api/v1/auth/")) {
      fireSessionExpired(path);
    }
    throw new Error(msg);
  }
  return (await res.json()) as T;
}

// Internal export so the server-API module can reuse the cookie+JSON
// machinery without duplicating fetch boilerplate.
export { request };

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
