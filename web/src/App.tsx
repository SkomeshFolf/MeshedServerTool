import { useEffect, useState } from "react";
import { Outlet, Link } from "react-router-dom";

interface Health {
  status: string;
  version: string;
}

export default function App() {
  const [health, setHealth] = useState<Health | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    fetch("/healthz")
      .then((r) => r.json())
      .then(setHealth)
      .catch((e) => setError(String(e)));
  }, []);

  return (
    <div className="app">
      <header className="topbar">
        <h1>Meshed Server Tool</h1>
        <nav>
          <Link to="/">Dashboard</Link>
        </nav>
        <div className="health">
          {health ? (
            <span className="ok">● {health.version}</span>
          ) : error ? (
            <span className="err">● backend offline</span>
          ) : (
            <span>● connecting…</span>
          )}
        </div>
      </header>
      <main>
        <Outlet />
      </main>
    </div>
  );
}
