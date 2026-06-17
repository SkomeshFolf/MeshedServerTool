import { useEffect, useState } from "react";
import { Link, useLocation, useNavigate } from "react-router-dom";
import { useAuth } from "./auth";
import type { Health } from "./types";
import "./styles.css";

export default function App() {
  const auth = useAuth();
  const location = useLocation();
  const navigate = useNavigate();
  const [health, setHealth] = useState<Health | null>(null);
  const [healthError, setHealthError] = useState<string | null>(null);

  useEffect(() => {
    fetch("/healthz")
      .then((r) => r.json())
      .then(setHealth)
      .catch((e) => setHealthError(String(e)));
  }, []);

  // While auth is still resolving, show a neutral state — never bounce the
  // user to /login prematurely (which would lose their deep link).
  if (auth.loading) {
    return (
      <div className="app">
        <main>
          <p className="muted">Loading…</p>
        </main>
      </div>
    );
  }

  if (!auth.authenticated) {
    return (
      <div className="app">
        <main>
          <p className="muted">Redirecting to sign in…</p>
        </main>
      </div>
    );
  }

  const handleLogout = async () => {
    await auth.logout();
    navigate("/login", { replace: true });
  };

  return (
    <div className="app">
      <header className="topbar">
        <h1>Meshed Server Tool</h1>
        <nav>
          <Link to="/">Dashboard</Link>
        </nav>
        <div className="user">
          <span className="muted">
            {auth.username} · {auth.role}
          </span>
          <button className="link" onClick={handleLogout}>
            Sign out
          </button>
        </div>
        <div className="health">
          {health ? (
            <span className="ok">● {health.version}</span>
          ) : healthError ? (
            <span className="err">● backend offline</span>
          ) : (
            <span>● connecting…</span>
          )}
        </div>
      </header>
      <main>
        <OutletWrapper pathname={location.pathname} />
      </main>
    </div>
  );
}

// App routes — kept tiny on purpose; Phase 2+ will add server list, detail,
// logs, reports. Today it's just the dashboard.
function OutletWrapper({ pathname }: { pathname: string }) {
  return <DashboardScreen pathname={pathname} />;
}

function DashboardScreen({ pathname }: { pathname: string }) {
  return (
    <section className="dashboard">
      <h2>Dashboard</h2>
      <p className="muted">
        Signed in. Path: <code>{pathname}</code>
      </p>
      <p className="muted">
        v3-dev. Phase 2 will add server list, start/stop controls, and
        live log tail.
      </p>
    </section>
  );
}
