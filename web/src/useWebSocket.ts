// useWebSocket — auto-reconnecting WebSocket hook.
//
// Connects to /api/v1/ws, decodes JSON events of shape {type, data},
// and passes them to the consumer's onEvent callback. Reconnects with
// exponential backoff (250ms → 8s) if the connection drops. Sends a
// ping every 25s to keep the connection alive through proxies.

import { useEffect, useRef } from "react";

export interface WSMessage {
  type: string;
  data: unknown;
}

export interface UseWebSocketOptions {
  onEvent: (msg: WSMessage) => void;
  onConnectionChange?: (connected: boolean) => void;
  /** Auto-reconnect (default: true) */
  reconnect?: boolean;
}

export function useWebSocket({ onEvent, onConnectionChange, reconnect = true }: UseWebSocketOptions): void {
  // Stash callbacks in refs so we can rebuild the connection without
  // re-triggering the effect on every render.
  const onEventRef = useRef(onEvent);
  const onConnRef = useRef(onConnectionChange);
  onEventRef.current = onEvent;
  onConnRef.current = onConnectionChange;

  useEffect(() => {
    let ws: WebSocket | null = null;
    let pingTimer: number | null = null;
    let reconnectTimer: number | null = null;
    let backoff = 250;
    let cancelled = false;

    const connect = () => {
      if (cancelled) return;
      const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
      const url = `${proto}//${window.location.host}/api/v1/ws`;
      ws = new WebSocket(url);

      ws.onopen = () => {
        backoff = 250; // reset on successful connect
        onConnRef.current?.(true);
        // Keepalive
        if (pingTimer !== null) window.clearInterval(pingTimer);
        pingTimer = window.setInterval(() => {
          if (ws && ws.readyState === WebSocket.OPEN) {
            ws.send(JSON.stringify({ type: "ping" }));
          }
        }, 25_000);
      };

      ws.onmessage = (ev) => {
        try {
          const msg = JSON.parse(ev.data) as WSMessage;
          onEventRef.current(msg);
        } catch (e) {
          // ignore malformed
        }
      };

      ws.onclose = () => {
        onConnRef.current?.(false);
        if (pingTimer !== null) {
          window.clearInterval(pingTimer);
          pingTimer = null;
        }
        if (reconnect && !cancelled) {
          reconnectTimer = window.setTimeout(connect, backoff);
          backoff = Math.min(backoff * 2, 8_000);
        }
      };

      ws.onerror = () => {
        // The close event will fire next; onerror just enables retry from there.
      };
    };

    connect();

    return () => {
      cancelled = true;
      if (pingTimer !== null) window.clearInterval(pingTimer);
      if (reconnectTimer !== null) window.clearTimeout(reconnectTimer);
      if (ws) {
        // Detach handlers so the close doesn't trigger our reconnect.
        ws.onclose = null;
        ws.close();
      }
    };
  }, [reconnect]);
}
