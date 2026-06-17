import { useState, type FormEvent } from "react";
import { Navigate, useLocation, useNavigate } from "react-router-dom";
import { useAuth } from "../auth";

export default function Login() {
  const auth = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const from = (location.state as { from?: string } | null)?.from ?? "/";

  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [submitting, setSubmitting] = useState(false);

  // Already logged in? Bounce home.
  if (!auth.loading && auth.authenticated) {
    return <Navigate to={from} replace />;
  }

  // Fresh install: only the bootstrap form is meaningful.
  if (!auth.loading && auth.bootstrapAvailable) {
    return (
      <BootstrapForm
        submitting={submitting}
        onSubmitting={setSubmitting}
        error={auth.error}
        clearError={auth.clearError}
        onSuccess={() => navigate(from, { replace: true })}
        submit={auth.bootstrap}
      />
    );
  }

  // Normal login path.
  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setSubmitting(true);
    try {
      await auth.login(username, password);
      navigate(from, { replace: true });
    } catch {
      // error already on auth state
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <section className="login">
      <h2>Sign in</h2>
      <form onSubmit={handleSubmit}>
        <label>
          Username
          <input
            type="text"
            name="username"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoComplete="username"
            autoFocus
            required
            disabled={submitting}
          />
        </label>
        <label>
          Password
          <input
            type="password"
            name="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="current-password"
            required
            disabled={submitting}
          />
        </label>
        {auth.error && <p className="error">{auth.error}</p>}
        <button type="submit" disabled={submitting || !username || !password}>
          {submitting ? "Signing in…" : "Sign in"}
        </button>
      </form>
      {auth.loading && <p className="muted">Loading…</p>}
    </section>
  );
}

interface BootstrapProps {
  submitting: boolean;
  onSubmitting: (v: boolean) => void;
  error: string | null;
  clearError: () => void;
  onSuccess: () => void;
  submit: (username: string, password: string) => Promise<void>;
}

function BootstrapForm({
  submitting,
  onSubmitting,
  error,
  clearError,
  onSuccess,
  submit,
}: BootstrapProps) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");

  const mismatch = confirm.length > 0 && password !== confirm;

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (mismatch) return;
    onSubmitting(true);
    try {
      await submit(username, password);
      onSuccess();
    } catch {
      // error already on auth state
    } finally {
      onSubmitting(false);
    }
  };

  return (
    <section className="login">
      <h2>First-time setup</h2>
      <p className="muted">
        Create the admin account. There are no users yet, so this is the only
        way in.
      </p>
      <form onSubmit={handleSubmit}>
        <label>
          Username
          <input
            type="text"
            name="username"
            value={username}
            onChange={(e) => {
              clearError();
              setUsername(e.target.value);
            }}
            autoComplete="username"
            autoFocus
            required
            disabled={submitting}
          />
        </label>
        <label>
          Password
          <input
            type="password"
            name="password"
            value={password}
            onChange={(e) => {
              clearError();
              setPassword(e.target.value);
            }}
            autoComplete="new-password"
            required
            minLength={8}
            disabled={submitting}
          />
        </label>
        <label>
          Confirm password
          <input
            type="password"
            name="confirm"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            autoComplete="new-password"
            required
            disabled={submitting}
          />
        </label>
        {mismatch && <p className="error">Passwords do not match</p>}
        {error && <p className="error">{error}</p>}
        <button
          type="submit"
          disabled={
            submitting ||
            !username ||
            password.length < 8 ||
            mismatch
          }
        >
          {submitting ? "Creating admin…" : "Create admin account"}
        </button>
      </form>
    </section>
  );
}
