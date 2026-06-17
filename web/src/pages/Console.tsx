// Console panel for the per-server detail page.
//
// Streams stdin writes to the running game server. The game server's
// stdout/stderr are already streamed through the WebSocket log viewer
// above this panel, so the operator gets the full round-trip
// (write → game echo) in the same context.
//
// Disabled when the server isn't running.
//
// Keyboard:
//   Enter           → send
//   Shift+Enter     → insert literal newline
//   Up/Down arrows  → cycle through the most recent sent lines
import { useEffect, useRef, useState } from "react";
import { consoleApi } from "../serversApi";

const MAX_INPUT = 1000; // hard cap, matches the server-side 1 KiB-ish limit
const HISTORY_LIMIT = 50;

export interface ConsoleProps {
  serverName: string;
  enabled: boolean;
}

export default function Console({ serverName, enabled }: ConsoleProps) {
  const [text, setText] = useState("");
  const [scrollback, setScrollback] = useState<string[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [sending, setSending] = useState(false);
  const [history, setHistory] = useState<string[]>([]);
  const [historyIdx, setHistoryIdx] = useState<number>(-1);
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);
  const scrollbackRef = useRef<HTMLDivElement | null>(null);

  // When the server is stopped/restarted, clear the local scrollback
  // since the new process is a different context. The "running" key
  // changes are signaled by the parent.
  useEffect(() => {
    if (!enabled) {
      setScrollback([]);
      setError(null);
    }
  }, [enabled]);

  // Auto-scroll the scrollback to the bottom when new lines arrive.
  useEffect(() => {
    const el = scrollbackRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [scrollback]);

  const send = async () => {
    if (!enabled) return;
    const trimmed = text.replace(/\s+$/, ""); // strip trailing whitespace
    if (trimmed === "") return;
    setError(null);
    setSending(true);
    // Optimistically add to local scrollback and history before the
    // network round-trip — the game server's echo will also show up
    // in the log pane, so the operator always sees confirmation.
    const stamp = new Date().toLocaleTimeString();
    setScrollback((prev) => [...prev, `[${stamp}] > ${trimmed}`]);
    setHistory((prev) => {
      const next = [trimmed, ...prev.filter((h) => h !== trimmed)];
      return next.slice(0, HISTORY_LIMIT);
    });
    setHistoryIdx(-1);
    try {
      // /stdin/line appends '\n' for us; we send the raw text.
      await consoleApi.sendLine(serverName, trimmed);
      setText("");
    } catch (e) {
      const msg = e instanceof Error ? e.message : "send failed";
      setError(msg);
      // Append an error marker to the scrollback so the operator
      // sees it inline with the rest of the session history.
      setScrollback((prev) => [...prev, `[${stamp}] ! error: ${msg}`]);
    } finally {
      setSending(false);
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      void send();
      return;
    }
    if (e.key === "ArrowUp" && history.length > 0) {
      e.preventDefault();
      const next = historyIdx < 0 ? 0 : Math.min(historyIdx + 1, history.length - 1);
      setHistoryIdx(next);
      setText(history[next] ?? "");
      return;
    }
    if (e.key === "ArrowDown" && historyIdx >= 0) {
      e.preventDefault();
      const next = historyIdx - 1;
      if (next < 0) {
        setHistoryIdx(-1);
        setText("");
      } else {
        setHistoryIdx(next);
        setText(history[next] ?? "");
      }
    }
  };

  return (
    <div className="console">
      <div className="console-header">
        <span className="muted">Console (stdin)</span>
        <span className="muted">
          {text.length}/{MAX_INPUT}
        </span>
      </div>
      <div className="console-scrollback" ref={scrollbackRef}>
        {scrollback.length === 0 ? (
          <span className="muted">
            {enabled
              ? "Type a command and press Enter to send. Shift+Enter for newline. Up/Down for history."
              : "Console disabled — server is not running."}
          </span>
        ) : (
          scrollback.map((line, i) => (
            <div key={i} className="console-line">
              {line}
            </div>
          ))
        )}
      </div>
      <div className="console-input-row">
        <textarea
          ref={textareaRef}
          value={text}
          onChange={(e) => {
            const v = e.target.value;
            if (v.length > MAX_INPUT) {
              setText(v.slice(0, MAX_INPUT));
            } else {
              setText(v);
            }
          }}
          onKeyDown={handleKeyDown}
          rows={2}
          disabled={!enabled || sending}
          placeholder={enabled ? "Enter to send · Shift+Enter for newline" : "start the server to use the console"}
          spellCheck={false}
          autoComplete="off"
        />
        <button
          onClick={() => void send()}
          disabled={!enabled || sending || text.trim() === ""}
        >
          {sending ? "Sending…" : "Send"}
        </button>
      </div>
      {error && <p className="error">Last error: {error}</p>}
    </div>
  );
}
