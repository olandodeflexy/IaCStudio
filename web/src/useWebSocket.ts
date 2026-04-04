import { useEffect, useRef, useCallback, useState } from 'react';

export interface WSMessage {
  type: 'file_changed' | 'terminal' | 'error';
  project?: string;
  file?: string;
  tool?: string;
  output?: string;
  error?: string;
}

export function useWebSocket(onMessage: (msg: WSMessage) => void) {
  const wsRef = useRef<WebSocket | null>(null);
  const [connected, setConnected] = useState(false);
  const reconnectTimer = useRef<ReturnType<typeof setTimeout>>();

  // Store the callback in a ref so that changes to onMessage don't
  // cause the WebSocket to disconnect and reconnect. Without this,
  // adding any dependency to the caller's useCallback (e.g., to read
  // current state) would trigger an infinite reconnect loop on every
  // render because connect → useEffect → cleanup → reconnect.
  const onMessageRef = useRef(onMessage);
  onMessageRef.current = onMessage;

  const connect = useCallback(() => {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${protocol}//${window.location.host}/ws`;

    const ws = new WebSocket(wsUrl);
    wsRef.current = ws;

    ws.onopen = () => {
      setConnected(true);
      console.log('[WS] Connected');
    };

    ws.onmessage = (event) => {
      try {
        const msg: WSMessage = JSON.parse(event.data);
        onMessageRef.current(msg);
      } catch (e) {
        console.warn('[WS] Invalid message:', event.data);
      }
    };

    ws.onclose = () => {
      setConnected(false);
      console.log('[WS] Disconnected, reconnecting in 3s...');
      reconnectTimer.current = setTimeout(connect, 3000);
    };

    ws.onerror = (err) => {
      console.error('[WS] Error:', err);
      ws.close();
    };
  }, []); // no dependencies — connect is stable

  useEffect(() => {
    connect();
    return () => {
      clearTimeout(reconnectTimer.current);
      wsRef.current?.close();
    };
  }, [connect]);

  const send = useCallback((data: any) => {
    if (wsRef.current?.readyState === WebSocket.OPEN) {
      wsRef.current.send(JSON.stringify(data));
    }
  }, []);

  return { connected, send };
}
