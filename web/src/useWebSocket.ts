import { useEffect, useRef, useCallback, useState } from 'react';

export interface WSMessage {
  type: 'file_changed' | 'terminal' | 'error' | 'ai_progress' | 'ai_topology_result';
  project?: string;
  file?: string;
  tool?: string;
  output?: string;
  error?: string;
  status?: string;
  message?: string;
  resources?: any[];
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

  const mounted = useRef(false);
  useEffect(() => {
    // Prevent React StrictMode double-mount from creating two connections.
    // Only connect if we don't already have an open/connecting socket.
    if (!mounted.current || !wsRef.current || wsRef.current.readyState > 1) {
      mounted.current = true;
      connect();
    }
    return () => {
      clearTimeout(reconnectTimer.current);
      // Don't close the socket on StrictMode cleanup — only close if
      // the component is truly unmounting (we check on next mount).
    };
  }, [connect]);

  const send = useCallback((data: any) => {
    if (wsRef.current?.readyState === WebSocket.OPEN) {
      wsRef.current.send(JSON.stringify(data));
    }
  }, []);

  return { connected, send };
}
