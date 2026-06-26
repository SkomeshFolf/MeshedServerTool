import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { reportsApi, bansApi, type Report } from "../serversApi";
import { useWebSocket, type WSMessage } from "../useWebSocket";
import { useToast } from "../toast";

export default function ReportsPage() {
  const toast = useToast();
  const [reports, setReports] = useState<Report[]>([]);
  const [perUser, setPerUser] = useState<Record<string, number>>({});
  const [filter, setFilter] = useState<"all" | "unhandled" | "handled">("unhandled");
  // `error` is for load failures only (background, not user-initiated).
  // Mutation failures surface as toasts instead.
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<number | null>(null);

  const load = () => {
    const params = filter === "all" ? {} : { handled: filter === "handled" };
    reportsApi.list(params)
      .then((d) => {
        setReports(d.reports);
        setPerUser(d.reports_per_user);
        setError(null);
      })
      .catch((e: unknown) => {
        const msg = e instanceof Error ? e.message : String(e);
        setError(msg);
        toast.error(`Failed to load reports: ${msg}`);
      });
  };

  useEffect(() => { load(); }, [filter]);

  // Live updates from WebSocket.
  useWebSocket({
    onEvent: (msg: WSMessage) => {
      if (msg.type === "report.new" || msg.type === "report.updated" || msg.type === "report.deleted") {
        load();
      }
    },
  });

  const handleMark = async (r: Report, handled: boolean) => {
    setBusy(r.id);
    try {
      await reportsApi.markHandled(r.id, handled);
      load();
      toast.success(handled ? `Report #${r.id} marked handled` : `Report #${r.id} reopened`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  };

  const handleBan = async (r: Report) => {
    setBusy(r.id);
    try {
      await bansApi.add({
        steam_id: r.target_id,
        player_name: r.target_name,
        reason: r.reason || `Reported by ${r.source_name}`,
      });
      await reportsApi.markHandled(r.id, true);
      load();
      toast.success(`Banned ${r.target_name || r.target_id} and handled report #${r.id}`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  };

  const handleDelete = async (r: Report) => {
    if (!window.confirm(`Delete report #${r.id}?`)) return;
    setBusy(r.id);
    try {
      await reportsApi.delete(r.id);
      load();
      toast.success(`Report #${r.id} deleted`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  };

  return (
    <section className="dashboard">
      <div className="row">
        <h2>Reports</h2>
        <div className="row right">
          <button
            className={filter === "unhandled" ? "" : "btn-ghost"}
            onClick={() => setFilter("unhandled")}
          >Unhandled</button>
          <button
            className={filter === "handled" ? "" : "btn-ghost"}
            onClick={() => setFilter("handled")}
          >Handled</button>
          <button
            className={filter === "all" ? "" : "btn-ghost"}
            onClick={() => setFilter("all")}
          >All</button>
        </div>
      </div>
      {error && <p className="error">{error}</p>}
      {reports.length === 0 ? (
        <p className="muted">No {filter === "all" ? "" : filter} reports.</p>
      ) : (
        <table className="reports">
          <thead>
            <tr>
              <th>When</th>
              <th>Server</th>
              <th>Target</th>
              <th>Source</th>
              <th>Reason</th>
              <th>Text</th>
              <th>Status</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {reports.map((r) => (
              <tr key={r.id} className={r.handled ? "row-handled" : "row-unhandled"}>
                <td className="muted">{r.date}</td>
                <td>
                  <Link to={`/servers/${encodeURIComponent(r.server_name)}`}>{r.server_name}</Link>
                </td>
                <td>{r.target_name} <span className="muted">({r.target_id})</span></td>
                <td>{r.source_name}</td>
                <td>{r.reason || <span className="muted">—</span>}</td>
                <td className="report-text">{r.text || <span className="muted">—</span>}</td>
                <td>
                  {r.handled ? (
                    <span className="pill pill-stopped">handled</span>
                  ) : (
                    <span className="pill pill-running">new</span>
                  )}
                  {r.target_id in perUser && perUser[r.target_id] > 1 && (
                    <span className="pill pill-warn" style={{ marginLeft: "0.5rem" }}>
                      {perUser[r.target_id]}× reports
                    </span>
                  )}
                </td>
                <td className="row-actions">
                  {!r.handled && (
                    <>
                      <button
                        onClick={() => handleMark(r, true)}
                        disabled={busy === r.id}
                      >Mark handled</button>
                      <button
                        className="btn-danger"
                        onClick={() => handleBan(r)}
                        disabled={busy === r.id}
                      >Ban + handle</button>
                    </>
                  )}
                  {r.handled && (
                    <button
                      className="btn-ghost"
                      onClick={() => handleMark(r, false)}
                      disabled={busy === r.id}
                    >Reopen</button>
                  )}
                  <button
                    className="btn-ghost"
                    onClick={() => handleDelete(r)}
                    disabled={busy === r.id}
                  >Delete</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}
