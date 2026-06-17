import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { aggregateApi, serversApi, type ChatEntry, type ServerView } from "../serversApi";

export default function AggregateChatsPage() {
  const [entries, setEntries] = useState<ChatEntry[]>([]);
  const [servers, setServers] = useState<ServerView[]>([]);
  const [filter, setFilter] = useState<string>("");
  const [error, setError] = useState<string | null>(null);
  const [updatedAt, setUpdatedAt] = useState<Date | null>(null);

  // 5s polling refresh; re-fetches whenever the filter changes.
  useEffect(() => {
    let mounted = true;
    let timer: number | null = null;

    const load = () => {
      aggregateApi
        .chats(200, filter || undefined)
        .then((es) => {
          if (!mounted) return;
          setEntries(es);
          setUpdatedAt(new Date());
          setError(null);
        })
        .catch((e: unknown) => {
          if (mounted) setError(e instanceof Error ? e.message : String(e));
        });
    };

    load();
    timer = window.setInterval(load, 5_000);
    return () => {
      mounted = false;
      if (timer !== null) window.clearInterval(timer);
    };
  }, [filter]);

  // Server dropdown data.
  useEffect(() => {
    let mounted = true;
    serversApi
      .list()
      .then((s) => mounted && setServers(s))
      .catch(() => {});
    return () => { mounted = false; };
  }, []);

  return (
    <section className="dashboard">
      <div className="row">
        <h2>Aggregate chat</h2>
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
          {entries.length} messages
          {updatedAt ? ` · refreshed ${updatedAt.toLocaleTimeString()}` : ""}
        </span>
      </div>

      {error && <p className="error">{error}</p>}

      {entries.length === 0 ? (
        <p className="muted">No chat messages yet.</p>
      ) : (
        <div className="chat-list">
          {entries.map((m) => (
            <div key={m.id} className="chat-line">
              <span className="muted chat-when">{m.at ? m.at.slice(11, 19) : ""}</span>
              <span className="muted">[{m.server_name}]</span>
              <span>
                <Link to={`/servers/${encodeURIComponent(m.server_name)}`} className="chat-name">
                  {m.player_name}:
                </Link>{" "}
                <span className="chat-text">{m.message}</span>
              </span>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
