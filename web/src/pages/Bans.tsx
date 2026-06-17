import { useEffect, useState } from "react";
import { bansApi, type Ban } from "../serversApi";
import { useWebSocket, type WSMessage } from "../useWebSocket";

export default function BansPage() {
  const [bans, setBans] = useState<Ban[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<number | null>(null);
  const [adding, setAdding] = useState(false);
  const [steamId, setSteamId] = useState("");
  const [playerName, setPlayerName] = useState("");
  const [reason, setReason] = useState("");

  const load = () => {
    bansApi.list()
      .then(setBans)
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)));
  };

  useEffect(() => { load(); }, []);

  useWebSocket({
    onEvent: (msg: WSMessage) => {
      if (msg.type === "ban.added" || msg.type === "ban.removed") load();
    },
  });

  const handleAdd = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(-1);
    setError(null);
    try {
      await bansApi.add({ steam_id: steamId, player_name: playerName, reason });
      setSteamId(""); setPlayerName(""); setReason("");
      setAdding(false);
      load();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  };

  const handleRemove = async (b: Ban) => {
    if (!window.confirm(`Unban ${b.player_name || b.steam_id}?`)) return;
    setBusy(b.id);
    try {
      await bansApi.remove(b.id);
      load();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  };

  return (
    <section className="dashboard">
      <div className="row">
        <h2>Global ban list</h2>
        <div className="row right">
          <button onClick={() => setAdding(!adding)}>
            {adding ? "Cancel" : "+ Ban player"}
          </button>
        </div>
      </div>
      {error && <p className="error">{error}</p>}
      {adding && (
        <form onSubmit={handleAdd} className="ban-form">
          <label>
            SteamID64
            <input
              type="text"
              value={steamId}
              onChange={(e) => setSteamId(e.target.value)}
              placeholder="76561198000000000"
              pattern="[0-9]{17}"
              required
              disabled={busy === -1}
            />
          </label>
          <label>
            Player name
            <input
              type="text"
              value={playerName}
              onChange={(e) => setPlayerName(e.target.value)}
              placeholder="optional"
              disabled={busy === -1}
            />
          </label>
          <label>
            Reason
            <input
              type="text"
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              placeholder="optional"
              disabled={busy === -1}
            />
          </label>
          <button type="submit" disabled={busy === -1 || !steamId}>
            {busy === -1 ? "Banning…" : "Add ban"}
          </button>
        </form>
      )}
      {bans.length === 0 ? (
        <p className="muted">No players are banned.</p>
      ) : (
        <table className="bans">
          <thead>
            <tr>
              <th>SteamID</th>
              <th>Name</th>
              <th>Reason</th>
              <th>Banned by</th>
              <th>When</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {bans.map((b) => (
              <tr key={b.id}>
                <td><code>{b.steam_id}</code></td>
                <td>{b.player_name || <span className="muted">—</span>}</td>
                <td>{b.reason || <span className="muted">—</span>}</td>
                <td>{b.banned_by || <span className="muted">—</span>}</td>
                <td className="muted">{b.banned_at}</td>
                <td>
                  <button
                    className="btn-ghost"
                    onClick={() => handleRemove(b)}
                    disabled={busy === b.id}
                  >Unban</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}
