import { useEffect, useMemo, useState } from "react";
import { aggregateApi, serversApi, type LogEntry, type ServerView } from "../serversApi";
import { useToast } from "../toast";

// Pick a stable, server-specific color so each server's lines stand
// out without us having to hand-tune a palette. Hash the server name
// into one of a small color set.
const SERVER_COLORS = [
  "#60a5fa", // blue
  "#4ade80", // green
  "#f59e0b", // amber
  "#f472b6", // pink
  "#a78bfa", // violet
  "#34d399", // teal
  "#fb7185", // rose
  "#facc15", // yellow
];

function colorFor(name: string): string {
  let h = 0;
  for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) >>> 0;
  return SERVER_COLORS[h % SERVER_COLORS.length];
}

export default function AggregateLogsPage() {
  const toast = useToast();
  const [entries, setEntries] = useState<LogEntry[]>([]);
  const [servers, setServers] = useState<ServerView[]>([]);
  const [filter, setFilter] = useState<string>("");
  // `error` is inline-display; toast mirrors load failures.
  const [error, setError] = useState<string | null>(null);
  const [updatedAt, setUpdatedAt] = useState<Date | null>(null);

  // Initial fetch + 5s polling. We keep filter and polling dependency-
  // free: re-fetch whenever the filter changes, but the timer is fixed.
  useEffect(() => {
    let mounted = true;
    let timer: number | null = null;

    const load = () => {
      aggregateApi
        .logs(200, filter || undefined)
        .then((es) => {
          if (!mounted) return;
          setEntries(es);
          setUpdatedAt(new Date());
          setError(null);
        })
        .catch((e: unknown) => {
          if (mounted) {
            const msg = e instanceof Error ? e.message : String(e);
            setError(msg);
            toast.error(`Failed to load logs: ${msg}`);
          }
        });
    };

    load();
    timer = window.setInterval(load, 5_000);
    return () => {
      mounted = false;
      if (timer !== null) window.clearInterval(timer);
    };
  }, [filter]);

  // Server list for the dropdown. Fetched once on mount.
  useEffect(() => {
    let mounted = true;
    serversApi
      .list()
      .then((s) => mounted && setServers(s))
      .catch(() => {});
    return () => { mounted = false; };
  }, []);

  const legend = useMemo(() => {
    const names = Array.from(new Set(entries.map((e) => e.server_name)));
    return names.sort();
  }, [entries]);

  return (
    <section className="dashboard">
      <div className="row">
        <h2>Aggregate logs</h2>
        <div className="row right">
          <label style={{ flexDirection: "row", alignItems: "center", gap: "0.5rem" }}>
            <span className="muted">Server</span>
            <select
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
            >
              <option value="">(all servers)</option>
              {servers.map((s) => (
                <option key={s.name} value={s.name}>{s.name}</option>
              ))}
            </select>
          </label>
        </div>
      </div>

      <div className="logs-status">
        <span className="muted">
          {entries.length} entries
          {updatedAt ? ` · refreshed ${updatedAt.toLocaleTimeString()}` : ""}
        </span>
        {legend.length > 0 && (
          <span style={{ marginLeft: "1rem", display: "inline-flex", gap: "0.5rem", flexWrap: "wrap" }}>
            {legend.map((n) => (
              <span
                key={n}
                className="pill"
                style={{ color: colorFor(n), borderColor: colorFor(n) }}
              >
                {n}
              </span>
            ))}
          </span>
        )}
      </div>

      {error && <p className="error">{error}</p>}

      <pre className="log-pane">
        {entries.length === 0 ? (
          <span className="muted">No log entries yet.</span>
        ) : (
          entries.map((e, i) => {
            const color = colorFor(e.server_name);
            return (
              <div
                key={`${e.at}-${i}`}
                className={`log-line log-${e.type ? "typed" : "stdout"}`}
                style={{ borderLeft: `3px solid ${color}`, paddingLeft: "0.5rem" }}
              >
                <span style={{ color }} className="log-type">[{e.server_name}]</span>
                {e.type && <span className="log-type">[{e.type}]</span>}{" "}
                <span className="muted">{e.at ? e.at.slice(11, 23) : ""}</span>{" "}
                {e.raw}
              </div>
            );
          })
        )}
      </pre>
    </section>
  );
}
