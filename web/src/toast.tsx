// Toast notification system.
//
// One global <ToastProvider /> mounted at the root, exposed via the
// useToast() hook. Toasts auto-dismiss after a per-toast timeout
// (default 4s for info/success, 6s for warnings, 8s for errors), stack
// in the top-right corner, and can be manually dismissed.
//
// Why a global provider instead of per-page state:
//   - User actions in one component (e.g. Console "send line" in a
//     server detail page) need feedback that's visible regardless of
//     where the user has navigated since.
//   - Background events (WebSocket disconnect/reconnect, login
//     session expiry) need a place to surface that isn't tied to a
//     specific component lifecycle.
//   - The previous per-page setError() pattern meant an error from a
//     form submission in /servers/new would vanish the moment the
//     user navigated away.

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";

export type ToastKind = "info" | "success" | "warn" | "error";

export interface Toast {
  /** Stable id for React keys and dismiss lookups. */
  id: number;
  kind: ToastKind;
  message: string;
  /** ms until auto-dismiss; 0 = sticky. Default depends on kind. */
  ttl: number;
}

interface ToastContextValue {
  toasts: Toast[];
  /** Push a toast. Returns the id so the caller can dismiss early. */
  show: (kind: ToastKind, message: string, ttl?: number) => number;
  /** Convenience wrappers — used in ~90% of call sites. */
  info: (message: string, ttl?: number) => number;
  success: (message: string, ttl?: number) => number;
  warn: (message: string, ttl?: number) => number;
  error: (message: string, ttl?: number) => number;
  dismiss: (id: number) => void;
}

const ToastContext = createContext<ToastContextValue | null>(null);

const DEFAULT_TTL: Record<ToastKind, number> = {
  info: 4000,
  success: 4000,
  warn: 6000,
  error: 8000,
};

let nextId = 1;

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  // Track active dismiss timers so we can clear them on manual dismiss
  // and on unmount (avoids "setState on unmounted" warnings and
  // prevents stale toasts from re-appearing).
  const timersRef = useRef<Map<number, number>>(new Map());

  const dismiss = useCallback((id: number) => {
    const t = timersRef.current.get(id);
    if (t !== undefined) {
      window.clearTimeout(t);
      timersRef.current.delete(id);
    }
    setToasts((prev) => prev.filter((x) => x.id !== id));
  }, []);

  const show = useCallback(
    (kind: ToastKind, message: string, ttl?: number) => {
      const id = nextId++;
      const finalTtl = ttl ?? DEFAULT_TTL[kind];
      const toast: Toast = { id, kind, message, ttl: finalTtl };
      setToasts((prev) => [...prev, toast]);
      if (finalTtl > 0) {
        const handle = window.setTimeout(() => dismiss(id), finalTtl);
        timersRef.current.set(id, handle);
      }
      return id;
    },
    [dismiss],
  );

  // Clear all pending timers on unmount.
  useEffect(() => {
    const timers = timersRef.current;
    return () => {
      timers.forEach((h) => window.clearTimeout(h));
      timers.clear();
    };
  }, []);

  const value: ToastContextValue = {
    toasts,
    show,
    info: (m, ttl) => show("info", m, ttl),
    success: (m, ttl) => show("success", m, ttl),
    warn: (m, ttl) => show("warn", m, ttl),
    error: (m, ttl) => show("error", m, ttl),
    dismiss,
  };

  return (
    <ToastContext.Provider value={value}>
      {children}
      <ToastViewport toasts={toasts} onDismiss={dismiss} />
    </ToastContext.Provider>
  );
}

export function useToast(): ToastContextValue {
  const ctx = useContext(ToastContext);
  if (!ctx) {
    throw new Error("useToast must be used inside <ToastProvider>");
  }
  return ctx;
}

function ToastViewport({
  toasts,
  onDismiss,
}: {
  toasts: Toast[];
  onDismiss: (id: number) => void;
}) {
  return (
    <div className="toast-viewport" aria-live="polite" aria-atomic="false">
      {toasts.map((t) => (
        <div key={t.id} className={`toast toast-${t.kind}`} role="status">
          <span className="toast-message">{t.message}</span>
          <button
            className="toast-close"
            onClick={() => onDismiss(t.id)}
            aria-label="Dismiss"
          >
            ×
          </button>
        </div>
      ))}
    </div>
  );
}
