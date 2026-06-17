import { useEffect, useState } from "react";
import { motdApi, type MOTD } from "../serversApi";

export default function MotdPage() {
  const [global, setGlobal] = useState<MOTD | null>(null);
  const [perServer, setPerServer] = useState<MOTD[]>([]);
  const [editing, setEditing] = useState<string | null>(null); // "*" or server name
  const [draft, setDraft] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const load = () => {
    motdApi.list()
      .then((d) => {
        setGlobal(d.global);
        setPerServer(d.servers || []);
        setError(null);
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)));
  };

  useEffect(() => { load(); }, []);

  const startEdit = (key: string, current: MOTD | undefined) => {
    setEditing(key);
    setDraft(current?.message || "");
    setEnabled(current?.enabled ?? true);
  };

  const save = async () => {
    setBusy(true);
    try {
      if (editing === "*") {
        await motdApi.setGlobal(draft);
      } else if (editing) {
        await motdApi.setForServer(editing, { message: draft, enabled });
      }
      setEditing(null);
      setDraft("");
      load();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const deleteOverride = async (serverName: string) => {
    if (!window.confirm(`Remove MOTD override for ${serverName}?`)) return;
    setBusy(true);
    try {
      await motdApi.deleteForServer(serverName);
      load();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="dashboard">
      <h2>Message of the day</h2>
      {error && <p className="error">{error}</p>}

      <div className="motd-section">
        <div className="row">
          <h3>Global default</h3>
          <div className="row right">
            <button onClick={() => startEdit("*", global || undefined)}>
              {global && global.message ? "Edit" : "+ Set global default"}
            </button>
          </div>
        </div>
        {global && global.message ? (
          <div className="motd-banner">
            {global.message}
            <span className="muted" style={{ marginLeft: "1rem", fontSize: "0.8rem" }}>
              (updated {global.updated_at} by {global.updated_by || "—"})
            </span>
          </div>
        ) : (
          <p className="muted">No global default set. Servers without an override will show no MOTD.</p>
        )}
      </div>

      <div className="motd-section">
        <h3>Per-server overrides</h3>
        {perServer.length === 0 ? (
          <p className="muted">No per-server overrides.</p>
        ) : (
          <table className="bans">
            <thead>
              <tr>
                <th>Server</th>
                <th>Message</th>
                <th>Status</th>
                <th>Updated</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {perServer.map((m) => (
                <tr key={m.server_name}>
                  <td><code>{m.server_name}</code></td>
                  <td>{m.message}</td>
                  <td>
                    {m.enabled ? (
                      <span className="pill pill-running">enabled</span>
                    ) : (
                      <span className="pill pill-stopped">disabled</span>
                    )}
                  </td>
                  <td className="muted">{m.updated_at}</td>
                  <td>
                    <button className="btn-ghost" onClick={() => startEdit(m.server_name || "", m)}>Edit</button>
                    <button className="btn-ghost" onClick={() => m.server_name && deleteOverride(m.server_name)}>
                      Remove
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {editing !== null && (
        <div className="modal-overlay" onClick={() => setEditing(null)}>
          <div className="modal" onClick={(e) => e.stopPropagation()}>
            <h3>Edit MOTD{editing === "*" ? " (global default)" : ` for ${editing}`}</h3>
            <textarea
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              rows={4}
              placeholder="Welcome to the server. Be respectful, follow the rules."
              style={{ width: "100%", padding: "0.5rem", background: "#0f172a", color: "#e2e8f0", border: "1px solid #334155", borderRadius: "0.25rem" }}
            />
            {editing !== "*" && (
              <label style={{ display: "flex", gap: "0.5rem", alignItems: "center", marginTop: "0.5rem" }}>
                <input
                  type="checkbox"
                  checked={enabled}
                  onChange={(e) => setEnabled(e.target.checked)}
                />
                Enabled
              </label>
            )}
            <div className="row right" style={{ marginTop: "1rem" }}>
              <button className="btn-ghost" onClick={() => setEditing(null)}>Cancel</button>
              <button onClick={save} disabled={busy}>{busy ? "Saving…" : "Save"}</button>
            </div>
          </div>
        </div>
      )}
    </section>
  );
}
