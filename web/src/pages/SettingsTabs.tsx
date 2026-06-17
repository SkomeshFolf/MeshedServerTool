import { useEffect, useMemo, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { tabsApi, type PlayersTab, type TabName } from "../serversApi";

// Per-server settings tabs page.
//
// v2 had five tabs (Map, Players, Server, Gameplay, Management). The
// Management tab edited fields that in v3 live on the `servers` row
// (name/install_dir/port/...) and are exposed through the existing
// detail-page edit form, so this page only hosts the four INI-driven
// tabs.
//
// Sub-tab selection lives in the query string (?tab=server) so deep
// links and back/forward work.

const TABS: { id: TabName; label: string }[] = [
  { id: "map", label: "Map" },
  { id: "players", label: "Players" },
  { id: "server", label: "Server" },
  { id: "gameplay", label: "Gameplay" },
];

// v2 had this master "bOverrideDefaults" toggle that disabled every
// other gameplay field when off. Preserve that UX.
const GAMEPLAY_MASTER_KEY = "bOverrideDefaults";

// Heuristic: keys starting with "b" + uppercase third char (UE style)
// are booleans. Also keys ending in `Enabled` / `Override`. We don't
// need to be perfect — the user can always flip the type if we guess
// wrong; we just use this to pick a default input control.
function looksLikeBoolKey(k: string): boolean {
  if (k === GAMEPLAY_MASTER_KEY) return true;
  if (k.startsWith("b") && k.length > 2 && k[1] >= "A" && k[1] <= "Z") {
    return true;
  }
  return /Enabled|Override$/i.test(k);
}

export default function SettingsTabsPage() {
  const { name } = useParams<{ name: string }>();
  const [params, setParams] = useSearchParams();
  const activeTab = (params.get("tab") as TabName | null) || "map";

  if (!name) {
    return (
      <section className="dashboard">
        <p>No server specified.</p>
      </section>
    );
  }

  return (
    <section className="dashboard">
      <div className="row">
        <h2>Settings — {name}</h2>
        <div className="row right">
          <Link to={`/servers/${encodeURIComponent(name)}`}>← Back to server</Link>
          <Link to={`/servers/${encodeURIComponent(name)}/settings`}>Advanced INI files</Link>
        </div>
      </div>
      <p className="muted">
        Edits to these tabs update the game's INI files in the server's install
        directory. Changes take effect on the next server restart.
      </p>

      <nav className="subtabs">
        {TABS.map((t) => (
          <button
            key={t.id}
            className={activeTab === t.id ? "btn" : "btn-ghost"}
            onClick={() => setParams({ tab: t.id })}
          >
            {t.label}
          </button>
        ))}
      </nav>

      {activeTab === "map" && <MapTab serverName={name} />}
      {activeTab === "players" && <PlayersTabPane serverName={name} />}
      {activeTab === "server" && <ServerTab serverName={name} />}
      {activeTab === "gameplay" && <GameplayTab serverName={name} />}
    </section>
  );
}

// --- shared: load+save + error display ---

function useEntries(serverName: string, tab: "map" | "server" | "gameplay") {
  const [entries, setEntries] = useState<Record<string, string> | null>(null);
  const [dirty, setDirty] = useState<Record<string, string> | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    setEntries(null);
    setDirty(null);
    setError(null);
    const api = tabsApi[tab];
    api.get(serverName)
      .then((d) => {
        setEntries(d);
        setDirty(d);
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)));
  }, [serverName, tab]);

  const isDirty = useMemo(() => dirty !== null && entries !== null && !shallowEq(dirty, entries), [dirty, entries]);

  const update = (k: string, v: string) => {
    setDirty((d) => ({ ...(d || {}), [k]: v }));
  };
  const remove = (k: string) => {
    setDirty((d) => {
      if (!d) return d;
      const cp = { ...d };
      delete cp[k];
      return cp;
    });
  };
  const add = () => {
    setDirty((d) => {
      // Find a free `new_key_N` slot.
      let i = 1;
      while (d && d[`new_key_${i}`] !== undefined) i++;
      return { ...(d || {}), [`new_key_${i}`]: "" };
    });
  };

  const save = async () => {
    if (!dirty) return;
    setBusy(true);
    setError(null);
    try {
      const api = tabsApi[tab];
      await api.put(serverName, dirty);
      setEntries(dirty);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const reset = () => {
    setDirty(entries);
  };

  return { entries, dirty, isDirty, error, busy, update, remove, add, save, reset };
}

function shallowEq(a: Record<string, string>, b: Record<string, string>): boolean {
  const ak = Object.keys(a);
  const bk = Object.keys(b);
  if (ak.length !== bk.length) return false;
  for (const k of ak) if (a[k] !== b[k]) return false;
  return true;
}

// --- map tab ---

function MapTab({ serverName }: { serverName: string }) {
  const { dirty, isDirty, error, busy, update, remove, add, save, reset } = useEntries(serverName, "map");
  if (!dirty) return <p className="muted">Loading…</p>;

  const rows = Object.keys(dirty).sort();

  return (
    <div className="tab-pane">
      {error && <p className="error">{error}</p>}
      <div className="ini-section">
        <h4>[Map]</h4>
        <table className="ini-table">
          <thead>
            <tr><th>Key</th><th>Value</th><th></th></tr>
          </thead>
          <tbody>
            {rows.length === 0 && (
              <tr><td colSpan={3} className="muted">No entries.</td></tr>
            )}
            {rows.map((k) => (
              <tr key={k}>
                <td><input value={k} disabled /></td>
                <td><input value={dirty[k]} onChange={(e) => update(k, e.target.value)} style={{ width: "100%" }} /></td>
                <td>
                  <button className="btn-ghost" onClick={() => remove(k)}>×</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        <button className="btn-ghost" onClick={add}>+ Add entry</button>
      </div>
      <div className="row right" style={{ marginTop: "1rem" }}>
        <button className="btn-ghost" onClick={reset} disabled={!isDirty || busy}>Reset</button>
        <button onClick={save} disabled={!isDirty || busy}>{busy ? "Saving…" : "Save"}</button>
      </div>
      <p className="muted">
        Note: <code>~</code> in a value is rewritten to <code>?</code> and <code>-</code> to <code>=</code> on save
        (v2 quirk for UE-level values).
      </p>
    </div>
  );
}

// --- server tab ---

function ServerTab({ serverName }: { serverName: string }) {
  const { dirty, isDirty, error, busy, update, remove, add, save, reset } = useEntries(serverName, "server");
  if (!dirty) return <p className="muted">Loading…</p>;

  const rows = Object.keys(dirty).sort();

  return (
    <div className="tab-pane">
      {error && <p className="error">{error}</p>}
      <div className="ini-section">
        <h4>[/Game/SCPPandemic/Blueprints/GI_PandemicGameInstance.GI_PandemicGameInstance_C]</h4>
        <p className="muted">GameplayConfig is managed by the Gameplay tab and is hidden here.</p>
        <table className="ini-table">
          <thead>
            <tr><th>Key</th><th>Value</th><th></th></tr>
          </thead>
          <tbody>
            {rows.length === 0 && (
              <tr><td colSpan={3} className="muted">No entries.</td></tr>
            )}
            {rows.map((k) => (
              <tr key={k}>
                <td><input value={k} disabled /></td>
                <td><input value={dirty[k]} onChange={(e) => update(k, e.target.value)} style={{ width: "100%" }} /></td>
                <td>
                  <button className="btn-ghost" onClick={() => remove(k)}>×</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        <button className="btn-ghost" onClick={add}>+ Add entry</button>
      </div>
      <div className="row right" style={{ marginTop: "1rem" }}>
        <button className="btn-ghost" onClick={reset} disabled={!isDirty || busy}>Reset</button>
        <button onClick={save} disabled={!isDirty || busy}>{busy ? "Saving…" : "Save"}</button>
      </div>
    </div>
  );
}

// --- gameplay tab ---

function GameplayTab({ serverName }: { serverName: string }) {
  const { dirty, isDirty, error, busy, update, remove, add, save, reset } = useEntries(serverName, "gameplay");
  if (!dirty) return <p className="muted">Loading…</p>;

  // When bOverrideDefaults is "false" (case-insensitive), v2 disabled
  // every other field. Mirror that here.
  const overrideVal = dirty[GAMEPLAY_MASTER_KEY] ?? "";
  const overrideOn = overrideVal.toLowerCase() === "true";

  const rows = Object.keys(dirty).sort();

  // For each row decide the input control based on key shape + value.
  const renderValueInput = (k: string, v: string) => {
    if (looksLikeBoolKey(k)) {
      // Render as a checkbox. Empty/missing → unchecked.
      const checked = v.toLowerCase() === "true";
      return (
        <input
          type="checkbox"
          checked={checked}
          onChange={(e) => update(k, e.target.checked ? "true" : "false")}
        />
      );
    }
    // Try to interpret as number for a friendlier input; fall back to text.
    if (v !== "" && !isNaN(Number(v))) {
      return (
        <input
          type="number"
          step="any"
          value={v}
          onChange={(e) => update(k, e.target.value)}
          style={{ width: "100%" }}
        />
      );
    }
    return (
      <input
        type="text"
        value={v}
        onChange={(e) => update(k, e.target.value)}
        style={{ width: "100%" }}
      />
    );
  };

  return (
    <div className="tab-pane">
      {error && <p className="error">{error}</p>}
      <div className="ini-section">
        <h4>Gameplay</h4>
        <p className="muted">
          Stored as <code>GameplayConfig=(k=v,k=v,…)</code> in the Game Instance section.
        </p>
        <table className="ini-table">
          <thead>
            <tr><th>Key</th><th>Value</th><th></th></tr>
          </thead>
          <tbody>
            {rows.length === 0 && (
              <tr><td colSpan={3} className="muted">No entries.</td></tr>
            )}
            {rows.map((k) => {
              const isMaster = k === GAMEPLAY_MASTER_KEY;
              const disabled = !isMaster && !overrideOn;
              return (
                <tr key={k} className={disabled ? "muted" : ""}>
                  <td>
                    <span style={{ fontFamily: "monospace" }}>{k}</span>
                    {isMaster && <span className="pill pill-running" style={{ marginLeft: "0.4rem" }}>master</span>}
                  </td>
                  <td>
                    <fieldset disabled={false} style={{ border: 0, padding: 0, margin: 0 }}>
                      {/* Fieldset disabled is per-fieldset; we use a per-row disabled style instead */}
                      <div style={{ opacity: disabled ? 0.5 : 1, pointerEvents: disabled ? "none" : "auto" }}>
                        {renderValueInput(k, dirty[k])}
                      </div>
                    </fieldset>
                  </td>
                  <td>
                    <button className="btn-ghost" onClick={() => remove(k)} disabled={isMaster}>×</button>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
        <button className="btn-ghost" onClick={add}>+ Add entry</button>
      </div>
      <div className="row right" style={{ marginTop: "1rem" }}>
        <button className="btn-ghost" onClick={reset} disabled={!isDirty || busy}>Reset</button>
        <button onClick={save} disabled={!isDirty || busy}>{busy ? "Saving…" : "Save"}</button>
      </div>
    </div>
  );
}

// --- players tab ---

function PlayersTabPane({ serverName }: { serverName: string }) {
  const [data, setData] = useState<PlayersTab | null>(null);
  const [dirty, setDirty] = useState<PlayersTab | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    setData(null);
    setDirty(null);
    setError(null);
    tabsApi.players.get(serverName)
      .then((d) => { setData(d); setDirty(d); })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)));
  }, [serverName]);

  const update = (k: keyof PlayersTab, ids: string[]) => {
    setDirty((d) => (d ? { ...d, [k]: ids } : d));
  };

  const save = async () => {
    if (!dirty) return;
    setBusy(true);
    setError(null);
    try {
      await tabsApi.players.put(serverName, dirty);
      setData(dirty);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };
  const reset = () => setDirty(data);

  if (!dirty) return <p className="muted">Loading…</p>;

  const isDirty = data !== null && dirty !== null && (
    !arrEq(dirty.admins, data.admins) ||
    !arrEq(dirty.owners, data.owners) ||
    !arrEq(dirty.whitelist, data.whitelist)
  );

  return (
    <div className="tab-pane">
      {error && <p className="error">{error}</p>}
      <p className="muted">
        One Steam ID per line. Files are written next to the game INI files
        (AdminIDs.ini, OwnerIDs.ini, WhitelistIDs.ini).
      </p>
      <div className="row three">
        <PlayerListEditor
          title="Admins"
          ids={dirty.admins}
          onChange={(ids) => update("admins", ids)}
        />
        <PlayerListEditor
          title="Owners"
          ids={dirty.owners}
          onChange={(ids) => update("owners", ids)}
        />
        <PlayerListEditor
          title="Whitelist"
          ids={dirty.whitelist}
          onChange={(ids) => update("whitelist", ids)}
        />
      </div>
      <div className="row right" style={{ marginTop: "1rem" }}>
        <button className="btn-ghost" onClick={reset} disabled={!isDirty || busy}>Reset</button>
        <button onClick={save} disabled={!isDirty || busy}>{busy ? "Saving…" : "Save"}</button>
      </div>
    </div>
  );
}

function PlayerListEditor({ title, ids, onChange }: {
  title: string;
  ids: string[];
  onChange: (ids: string[]) => void;
}) {
  const text = ids.join("\n");
  return (
    <label className="player-list">
      <strong>{title}</strong>
      <textarea
        rows={10}
        value={text}
        onChange={(e) => onChange(e.target.value.split("\n"))}
        placeholder={"76561198000000001"}
      />
      <span className="muted">{ids.filter((s) => s.trim() !== "").length} ids</span>
    </label>
  );
}

function arrEq(a: string[], b: string[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false;
  return true;
}
