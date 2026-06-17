import { useEffect, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useAuth } from "./auth";
import { serversApi, type ServerView } from "./serversApi";
import type { Health } from "./types";
import "./styles.css";

export default function App() {
  const auth = useAuth();
  const navigate = useNavigate();
  const [health, setHealth] = useState<Health | null>(null);
  const [healthError, setHealthError] = useState<string | null>(null);
  const [servers, setServers] = useState<ServerView[]>([]);
  const [serversError, setServersError] = useState<string | null>(null);

  // Polling loop for the dashboard — replaced with WebSockets in Phase 3.
  useEffect(() => {
    let mounted = true;
    const load = () => {
      serversApi
        .list()
        .then((s: ServerView[]) => {
          if (mounted) {
            setServers(s);
            setServersError(null);
          }
        })
        .catch((e: unknown) => {
          if (mounted) {
            setServersError(e instanceof Error ? e.message : String(e));
          }
        });
    };
    load();
    const id = window.setInterval(load, 2000);
    return () => {
      mounted = false;
      window.clearInterval(id);
    };
  }, []);

  useEffect(() => {
    fetch("/healthz")
      .then((r) => r.json())
      .then(setHealth)
      .catch((e) => setHealthError(String(e)));
  }, []);

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
        <h1>
          <Link to="/" className="brand">Meshed Server Tool</Link>
        </h1>
        <nav>
          <Link to="/">Dashboard</Link>
          <Link to="/servers/new">Add server</Link>
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
        <OutletWrapper servers={servers} serversError={serversError} />
      </main>
    </div>
  );
}

// Router-aware content area. Renders different pages based on the URL.
// Using a single component here (rather than a deep <Outlet /> tree) keeps
// the v3.0 router footprint small; we can split into separate route
// components when pages grow beyond a screenful.
function OutletWrapper({
  servers,
  serversError,
}: {
  servers: ServerView[];
  serversError: string | null;
}) {
  const path = window.location.pathname;
  if (path === "/servers/new") return <CreateServerPage />;
  const detailMatch = /^\/servers\/([^/]+)$/.exec(path);
  if (detailMatch) return <ServerDetailPage name={detailMatch[1]} />;
  return <DashboardPage servers={servers} serversError={serversError} />;
}

function statusLabel(s: ServerView["state"]["status"]): string {
  return s.charAt(0).toUpperCase() + s.slice(1);
}

function StatusPill({ status }: { status: ServerView["state"]["status"] }) {
  return <span className={`pill pill-${status}`}>{statusLabel(status)}</span>;
}

function DashboardPage({
  servers,
  serversError,
}: {
  servers: ServerView[];
  serversError: string | null;
}) {
  if (serversError) {
    return (
      <section className="dashboard">
        <h2>Dashboard</h2>
        <p className="error">Failed to load servers: {serversError}</p>
      </section>
    );
  }
  return (
    <section className="dashboard">
      <div className="row">
        <h2>Servers</h2>
        <Link to="/servers/new" className="btn">+ Add server</Link>
      </div>
      {servers.length === 0 ? (
        <p className="muted">
          No servers yet. <Link to="/servers/new">Add one</Link> to get started.
        </p>
      ) : (
        <table className="servers">
          <thead>
            <tr>
              <th>Name</th>
              <th>Status</th>
              <th>Players</th>
              <th>Port</th>
              <th>Map</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {servers.map((s) => (
              <tr key={s.name}>
                <td>
                  <Link to={`/servers/${encodeURIComponent(s.name)}`}>{s.name}</Link>
                </td>
                <td><StatusPill status={s.state.status} /></td>
                <td>
                  {s.state.current_users}/{s.max_players}
                </td>
                <td>{s.port}</td>
                <td className="muted">{s.state.current_map || "—"}</td>
                <td>
                  <Link to={`/servers/${encodeURIComponent(s.name)}`}>Manage</Link>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}

function CreateServerPage() {
  const navigate = useNavigate();
  const [name, setName] = useState("");
  const [installDir, setInstallDir] = useState("");
  const [port, setPort] = useState(7777);
  const [maxPlayers, setMaxPlayers] = useState(32);
  const [hostname, setHostname] = useState("");
  const [executable, setExecutable] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      const args: Record<string, unknown> = {};
      if (executable) args.executable = executable;
      const created = await serversApi.create({
        name,
        install_dir: installDir,
        port,
        max_players: maxPlayers,
        hostname: hostname || "",
        args,
        autostart: false,
      });
      navigate(`/servers/${encodeURIComponent(created.name)}`, { replace: true });
    } catch (e) {
      setError(e instanceof Error ? e.message : "create failed");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <section className="dashboard narrow">
      <h2>Add server</h2>
      <p className="muted">
        Phase 2 stores server config and a generic executable path. A future
        phase will add a command template for the SCP: 5k / SCP Pandemic
        dedicated server binary.
      </p>
      <form onSubmit={handleSubmit}>
        <label>
          Name
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="e.g. main-pvp"
            required
            pattern="[A-Za-z0-9_\-]{3,64}"
            title="3-64 chars: letters, digits, _ and -"
            disabled={submitting}
          />
        </label>
        <label>
          Install directory
          <input
            value={installDir}
            onChange={(e) => setInstallDir(e.target.value)}
            placeholder="C:/SteamCMD/steamapps/common/SCP Pandemic Dedicated Server"
            required
            disabled={submitting}
          />
        </label>
        <label>
          Executable path
          <input
            value={executable}
            onChange={(e) => setExecutable(e.target.value)}
            placeholder="leave blank to register the server without starting it"
            disabled={submitting}
          />
        </label>
        <div className="row two">
          <label>
            Port
            <input
              type="number"
              value={port}
              onChange={(e) => setPort(parseInt(e.target.value, 10) || 0)}
              min={1}
              max={65535}
              disabled={submitting}
            />
          </label>
          <label>
            Max players
            <input
              type="number"
              value={maxPlayers}
              onChange={(e) => setMaxPlayers(parseInt(e.target.value, 10) || 0)}
              min={1}
              max={256}
              disabled={submitting}
            />
          </label>
        </div>
        <label>
          Hostname (optional)
          <input
            value={hostname}
            onChange={(e) => setHostname(e.target.value)}
            disabled={submitting}
          />
        </label>
        {error && <p className="error">{error}</p>}
        <div className="row right">
          <Link to="/" className="btn btn-ghost">Cancel</Link>
          <button type="submit" disabled={submitting || !name || !installDir}>
            {submitting ? "Creating…" : "Create server"}
          </button>
        </div>
      </form>
    </section>
  );
}

function ServerDetailPage({ name }: { name: string }) {
  const [server, setServer] = useState<ServerView | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const navigate = useNavigate();

  // Poll server state
  useEffect(() => {
    let mounted = true;
    const load = () => {
      serversApi
        .get(name)
        .then((s: ServerView) => mounted && setServer(s))
        .catch((e: unknown) => mounted && setError(e instanceof Error ? e.message : String(e)));
    };
    load();
    const id = window.setInterval(load, 2000);
    return () => {
      mounted = false;
      window.clearInterval(id);
    };
  }, [name]);

  const action = async (kind: "start" | "stop" | "restart" | "delete") => {
    setBusy(kind);
    setError(null);
    try {
      if (kind === "delete") {
        if (!window.confirm(`Delete server "${name}"? This cannot be undone.`)) {
          setBusy(null);
          return;
        }
        await serversApi.delete(name);
        navigate("/", { replace: true });
        return;
      }
      const fn = serversApi[kind];
      const updated = await fn(name);
      setServer(updated);
    } catch (e) {
      setError(e instanceof Error ? e.message : `${kind} failed`);
    } finally {
      setBusy(null);
    }
  };

  if (error && !server) {
    return (
      <section className="dashboard">
        <p className="error">{error}</p>
        <p><Link to="/">← Back to dashboard</Link></p>
      </section>
    );
  }
  if (!server) {
    return (
      <section className="dashboard">
        <p className="muted">Loading…</p>
      </section>
    );
  }

  const isRunning = server.state.status === "running" || server.state.status === "starting";

  return (
    <section className="dashboard">
      <div className="row">
        <h2>
          {server.name} <StatusPill status={server.state.status} />
        </h2>
        <div className="row right">
          {!isRunning ? (
            <button onClick={() => action("start")} disabled={busy !== null}>
              {busy === "start" ? "Starting…" : "Start"}
            </button>
          ) : (
            <>
              <button onClick={() => action("restart")} disabled={busy !== null}>
                {busy === "restart" ? "Restarting…" : "Restart"}
              </button>
              <button onClick={() => action("stop")} disabled={busy !== null}>
                {busy === "stop" ? "Stopping…" : "Stop"}
              </button>
            </>
          )}
          <button
            className="btn-danger"
            onClick={() => action("delete")}
            disabled={busy !== null}
          >
            {busy === "delete" ? "Deleting…" : "Delete"}
          </button>
        </div>
      </div>

      <dl className="kv">
        <dt>Install directory</dt>
        <dd><code>{server.install_dir}</code></dd>
        <dt>Port</dt>
        <dd>{server.port}</dd>
        <dt>Max players</dt>
        <dd>{server.max_players}</dd>
        <dt>Hostname</dt>
        <dd>{server.hostname || "—"}</dd>
        <dt>Status</dt>
        <dd>
          {server.state.status}
          {server.state.pid ? ` (pid ${server.state.pid})` : ""}
        </dd>
        <dt>Started at</dt>
        <dd>{server.state.started_at || "—"}</dd>
        <dt>Last exit code</dt>
        <dd>{server.state.last_exit_code ?? "—"}</dd>
        {error && (
          <>
            <dt>Last action error</dt>
            <dd className="error">{error}</dd>
          </>
        )}
      </dl>

      <h3>Logs</h3>
      <LogViewer name={name} />

      <p style={{ marginTop: "2rem" }}>
        <Link to="/">← Back to dashboard</Link>
      </p>
    </section>
  );
}

function LogViewer({ name }: { name: string }) {
  const [lines, setLines] = useState<import("./serversApi").LogLine[]>([]);
  const [streamError, setStreamError] = useState<string | null>(null);
  const [streamConnected, setStreamConnected] = useState(false);

  // 1. Snapshot on mount so we have something to show immediately.
  useEffect(() => {
    let mounted = true;
    serversApi.logsTail(name, 200).then((l: import("./serversApi").LogLine[]) => {
      if (mounted) setLines(l);
    }).catch((e: unknown) => {
      if (mounted) setStreamError(e instanceof Error ? e.message : String(e));
    });
    return () => { mounted = false; };
  }, [name]);

  // 2. Subscribe to the SSE stream for live updates.
  useEffect(() => {
    const es = new EventSource(serversApi.logsStreamURL(name), { withCredentials: true } as any);
    es.addEventListener("open", () => setStreamConnected(true));
    es.addEventListener("error", () => setStreamConnected(false));
    es.addEventListener("line", (ev) => {
      try {
        const line = JSON.parse((ev as MessageEvent).data) as import("./serversApi").LogLine;
        setLines((prev) => {
          const next = [...prev, line];
          if (next.length > 1000) next.splice(0, next.length - 1000);
          return next;
        });
      } catch (e) {
        setStreamError(e instanceof Error ? e.message : "parse error");
      }
    });
    return () => es.close();
  }, [name]);

  return (
    <div className="logs">
      <div className="logs-status">
        {streamConnected ? (
          <span className="ok">● live</span>
        ) : (
          <span className="muted">● disconnected</span>
        )}
        {streamError && <span className="err"> · {streamError}</span>}
        <span className="muted" style={{ marginLeft: "0.5rem" }}>
          {lines.length} lines
        </span>
      </div>
      <pre className="log-pane">
        {lines.length === 0 ? (
          <span className="muted">No log output yet.</span>
        ) : (
          lines.map((l, i) => (
            <div key={i} className={`log-line log-${l.stream}${l.type ? " log-typed" : ""}`}>
              {l.type && <span className="log-type">[{l.type}]</span>} {l.text}
            </div>
          ))
        )}
      </pre>
    </div>
  );
}
