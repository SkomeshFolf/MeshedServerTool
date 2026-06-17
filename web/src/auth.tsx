import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from "react";
import { api, type Role } from "./api";

export interface AuthState {
  /** True until the first /auth/status response has come back. */
  loading: boolean;
  /** True if a user is currently logged in. */
  authenticated: boolean;
  /** The current user's role, or null if not logged in. */
  role: Role | null;
  /** The current user's username, or null if not logged in. */
  username: string | null;
  /** True on a fresh install with no users yet — show the bootstrap form. */
  bootstrapAvailable: boolean;
  /** Last login/bootstrap error message, cleared on next attempt. */
  error: string | null;
}

export interface AuthActions {
  login: (username: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
  bootstrap: (username: string, password: string) => Promise<void>;
  clearError: () => void;
}

const AuthContext = createContext<(AuthState & AuthActions) | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<AuthState>({
    loading: true,
    authenticated: false,
    role: null,
    username: null,
    bootstrapAvailable: false,
    error: null,
  });

  const refresh = useCallback(async () => {
    try {
      const s = await api.status();
      setState({
        loading: false,
        authenticated: s.authenticated,
        role: s.role ?? null,
        username: s.username ?? null,
        bootstrapAvailable: s.bootstrap_available,
        error: null,
      });
    } catch (e) {
      setState((prev) => ({
        ...prev,
        loading: false,
        error: e instanceof Error ? e.message : String(e),
      }));
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const login = useCallback(
    async (username: string, password: string) => {
      setState((s) => ({ ...s, error: null }));
      try {
        await api.login(username, password);
        await refresh();
      } catch (e) {
        setState((s) => ({
          ...s,
          error: e instanceof Error ? e.message : "login failed",
        }));
        throw e;
      }
    },
    [refresh],
  );

  const logout = useCallback(async () => {
    setState((s) => ({ ...s, error: null }));
    try {
      await api.logout();
    } catch (e) {
      setState((s) => ({
        ...s,
        error: e instanceof Error ? e.message : "logout failed",
      }));
    }
    await refresh();
  }, [refresh]);

  const bootstrap = useCallback(
    async (username: string, password: string) => {
      setState((s) => ({ ...s, error: null }));
      try {
        await api.bootstrap(username, password);
        await refresh();
      } catch (e) {
        setState((s) => ({
          ...s,
          error: e instanceof Error ? e.message : "bootstrap failed",
        }));
        throw e;
      }
    },
    [refresh],
  );

  const clearError = useCallback(() => {
    setState((s) => ({ ...s, error: null }));
  }, []);

  return (
    <AuthContext.Provider
      value={{ ...state, login, logout, bootstrap, clearError }}
    >
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth(): AuthState & AuthActions {
  const ctx = useContext(AuthContext);
  if (!ctx) {
    throw new Error("useAuth must be used inside <AuthProvider>");
  }
  return ctx;
}
