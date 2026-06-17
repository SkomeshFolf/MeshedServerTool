import { useEffect, useState } from "react";
import { useParams, Link } from "react-router-dom";
import { settingsApi, type INIFile } from "../serversApi";

export default function SettingsPage() {
  const { name } = useParams<{ name: string }>();
  const [files, setFiles] = useState<string[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [ini, setIni] = useState<INIFile | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [syncMsg, setSyncMsg] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // List INI files in the install dir.
  useEffect(() => {
    if (!name) return;
    settingsApi.list(name)
      .then((d) => setFiles(d.files || []))
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)));
  }, [name]);

  // Load a file when selected.
  useEffect(() => {
    if (!name || !selected) return;
    setBusy(true);
    settingsApi.read(name, selected)
      .then(setIni)
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setBusy(false));
  }, [name, selected]);

  const save = async () => {
    if (!name || !selected || !ini) return;
    setBusy(true);
    try {
      await settingsApi.write(name, selected, ini);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const syncBans = async () => {
    if (!name) return;
    setBusy(true);
    try {
      const r = await settingsApi.syncBans(name);
      setSyncMsg(`Synced ${r.ban_count} bans to ${r.path}`);
      // Refresh file list and re-read BannedIDs.ini if visible
      const list = await settingsApi.list(name);
      setFiles(list.files || []);
      if (selected === "BannedIDs.ini") {
        const f = await settingsApi.read(name, selected);
        setIni(f);
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const updateEntry = (sIdx: number, eIdx: number, key: string, value: string) => {
    if (!ini) return;
    const sections = ini.sections.map((s, i) => {
      if (i !== sIdx) return s;
      const entries = s.entries.map((e, j) => j === eIdx ? { ...e, key, value } : e);
      return { ...s, entries };
    });
    setIni({ ...ini, sections });
  };

  const addEntry = (sIdx: number) => {
    if (!ini) return;
    const sections = ini.sections.map((s, i) => {
      if (i !== sIdx) return s;
      return { ...s, entries: [...s.entries, { key: "", value: "" }] };
    });
    setIni({ ...ini, sections });
  };

  const removeEntry = (sIdx: number, eIdx: number) => {
    if (!ini) return;
    const sections = ini.sections.map((s, i) => {
      if (i !== sIdx) return s;
      return { ...s, entries: s.entries.filter((_, j) => j !== eIdx) };
    });
    setIni({ ...ini, sections });
  };

  if (!name) return <section className="dashboard"><p>No server specified.</p></section>;

  return (
    <section className="dashboard">
      <div className="row">
        <h2>Settings — {name}</h2>
        <div className="row right">
          <button onClick={syncBans} disabled={busy} title="Write the global ban list into BannedIDs.ini">
            Sync bans → BannedIDs.ini
          </button>
          <Link to={`/servers/${encodeURIComponent(name)}`}>← Back to server</Link>
        </div>
      </div>

      {error && <p className="error">{error}</p>}
      {syncMsg && <p className="muted">{syncMsg}</p>}

      <div className="settings-layout">
        <aside className="settings-files">
          <h3>Files</h3>
          {files.length === 0 ? (
            <p className="muted">No .ini files in install dir.</p>
          ) : (
            <ul>
              {files.map((f) => (
                <li key={f}>
                  <button
                    className={selected === f ? "" : "btn-ghost"}
                    onClick={() => setSelected(f)}
                    style={{ textAlign: "left", width: "100%" }}
                  >
                    {f}
                  </button>
                </li>
              ))}
            </ul>
          )}
        </aside>

        <main className="settings-content">
          {!selected && <p className="muted">Pick a file from the sidebar.</p>}
          {selected && ini && (
            <>
              <div className="row">
                <h3>{ini.rel_path}</h3>
                <div className="row right">
                  <button className="btn-ghost" onClick={() => setSelected(null)}>Close</button>
                  <button onClick={save} disabled={busy}>{busy ? "Saving…" : "Save"}</button>
                </div>
              </div>
              {ini.sections.length === 0 ? (
                <p className="muted">Empty file.</p>
              ) : (
                ini.sections.map((s, sIdx) => (
                  <div key={sIdx} className="ini-section">
                    <h4>[{s.name}]</h4>
                    <table className="ini-table">
                      <thead>
                        <tr><th>Key</th><th>Value</th><th></th></tr>
                      </thead>
                      <tbody>
                        {s.entries.map((e, eIdx) => (
                          <tr key={eIdx}>
                            <td><input value={e.key} onChange={(ev) => updateEntry(sIdx, eIdx, ev.target.value, e.value)} /></td>
                            <td><input value={e.value} onChange={(ev) => updateEntry(sIdx, eIdx, e.key, ev.target.value)} style={{ width: "100%" }} /></td>
                            <td>
                              <button className="btn-ghost" onClick={() => removeEntry(sIdx, eIdx)}>×</button>
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                    <button className="btn-ghost" onClick={() => addEntry(sIdx)}>+ Add entry</button>
                  </div>
                ))
              )}
            </>
          )}
        </main>
      </div>
    </section>
  );
}
