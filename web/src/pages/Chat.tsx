import { useEffect, useState } from "react";
import { useParams } from "react-router-dom";
import { chatApi, type ChatMessage } from "../serversApi";
import { useWebSocket, type WSMessage } from "../useWebSocket";
import { serversApi, type LogLine } from "../serversApi";
import { useToast } from "../toast";

export default function ChatPage() {
  const { name } = useParams<{ name: string }>();
  const toast = useToast();
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [serverExists, setServerExists] = useState<boolean | null>(null);
  // `error` is the inline display; toast mirrors it so the user sees
  // the failure even if they navigate away.
  const [error, setError] = useState<string | null>(null);

  // Verify server exists, then load history.
  useEffect(() => {
    if (!name) return;
    serversApi.get(name)
      .then(() => setServerExists(true))
      .catch(() => setServerExists(false));
    chatApi.list(name, 500)
      .then(setMessages)
      .catch((e: unknown) => {
        const msg = e instanceof Error ? e.message : String(e);
        setError(msg);
        toast.error(`Failed to load chat: ${msg}`);
      });
  }, [name]);

  // Live updates: append chat-typed log lines to the list.
  useWebSocket({
    onEvent: (msg: WSMessage) => {
      if (msg.type !== "log.line") return;
      const d = msg.data as { server_name: string; line: LogLine };
      if (d.server_name !== name) return;
      if (d.line.type !== "chat" && d.line.type !== "chat_simple") return;
      const player = (d.line.fields as Record<string, string>)?.name ?? "?";
      const text = (d.line.fields as Record<string, string>)?.message ?? d.line.text;
      setMessages((prev) => [
        {
          id: -Date.now(), // negative so we don't collide with real ids
          server_name: name ?? "",
          player_name: player,
          message: text,
          at: d.line.at,
        },
        ...prev,
      ]);
    },
  });

  if (!name) return <section className="dashboard"><p>No server specified.</p></section>;
  if (serverExists === false) {
    return (
      <section className="dashboard">
        <p className="error">Server "{name}" not found.</p>
      </section>
    );
  }

  return (
    <section className="dashboard">
      <h2>Chat — {name}</h2>
      {error && <p className="error">{error}</p>}
      {messages.length === 0 ? (
        <p className="muted">No chat messages yet.</p>
      ) : (
        <div className="chat-list">
          {messages.map((m) => (
            <div key={m.id} className="chat-line">
              <span className="muted chat-when">{m.at.slice(11, 19)}</span>
              <span className="chat-name">{m.player_name}:</span>
              <span className="chat-text">{m.message}</span>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
