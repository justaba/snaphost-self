import { useEffect, useRef, useState } from 'react';
import { auth } from '@/entities/session/@x/deploy';
import { api } from '@/shared/api/http';
import type { LogLine, LogsHistoryResponse } from '../model/types';

const MAX_LINES = 5000;
const POLL_INTERVAL_MS = 3000;
const MAX_RECONNECT_DELAY_MS = 30_000;

export interface UseDeployLogsResult {
  logs: LogLine[];
  isConnected: boolean;
  isLoading: boolean;
  error: string | null;
}

function deriveWebSocketUrl(deployId: string, override?: string | null): string {
  if (override && override.length > 0) return override;
  const baseHttp = import.meta.env.VITE_API_URL ?? '';
  if (baseHttp.startsWith('http://')) {
    return `${baseHttp.replace(/^http:\/\//, 'ws://')}/ws/logs/${deployId}`;
  }
  if (baseHttp.startsWith('https://')) {
    return `${baseHttp.replace(/^https:\/\//, 'wss://')}/ws/logs/${deployId}`;
  }
  const proto = window.location.protocol === 'https:' ? 'wss' : 'ws';
  return `${proto}://${window.location.host}/ws/logs/${deployId}`;
}

function appendCapped(prev: LogLine[], next: LogLine[]): LogLine[] {
  const merged = [...prev, ...next];
  if (merged.length <= MAX_LINES) return merged;
  return merged.slice(merged.length - MAX_LINES);
}

export function useDeployLogs(
  deployId: string | null | undefined,
  websocketUrl?: string | null,
): UseDeployLogsResult {
  const [logs, setLogs] = useState<LogLine[]>([]);
  const [isConnected, setIsConnected] = useState(false);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const wsRef = useRef<WebSocket | null>(null);
  const pollTimerRef = useRef<number | null>(null);
  const reconnectTimerRef = useRef<number | null>(null);
  const reconnectAttemptRef = useRef(0);
  const historyCursorRef = useRef<string | null>(null);
  const historyLoadedRef = useRef(false);
  const cancelledRef = useRef(false);

  useEffect(() => {
    if (!deployId) return;

    cancelledRef.current = false;
    setLogs([]);
    setIsConnected(false);
    setError(null);
    setIsLoading(true);
    historyLoadedRef.current = false;
    historyCursorRef.current = null;
    reconnectAttemptRef.current = 0;

    const clearReconnectTimer = () => {
      if (reconnectTimerRef.current !== null) {
        window.clearTimeout(reconnectTimerRef.current);
        reconnectTimerRef.current = null;
      }
    };

    const stopPolling = () => {
      if (pollTimerRef.current !== null) {
        window.clearInterval(pollTimerRef.current);
        pollTimerRef.current = null;
      }
    };

    const ingest = (incoming: LogLine[]) => {
      if (incoming.length === 0) return;
      setLogs((prev) => appendCapped(prev, incoming));
    };

    const fetchHistory = async () => {
      try {
        const since = historyCursorRef.current;
        const qs = new URLSearchParams();
        if (since) qs.set('since', since);
        const url = `/api/v1/deploys/${deployId}/logs${qs.toString() ? `?${qs.toString()}` : ''}`;
        const data = await api.get<LogsHistoryResponse>(url);
        if (cancelledRef.current) return;
        const items: LogLine[] = data.entries.map((entry) => ({
          ...entry.line,
          deploy_id: entry.line.deploy_id ?? deployId,
        }));
        historyCursorRef.current = data.next_since;
        ingest(items);
      } catch (err) {
        if (cancelledRef.current) return;
        const message = err instanceof Error ? err.message : 'Не удалось загрузить логи';
        setError(message);
      }
    };

    const startPolling = () => {
      if (pollTimerRef.current !== null) return;
      pollTimerRef.current = window.setInterval(() => {
        void fetchHistory();
      }, POLL_INTERVAL_MS);
    };

    const scheduleReconnect = () => {
      const attempt = reconnectAttemptRef.current;
      const delay = Math.min(1000 * 2 ** attempt, MAX_RECONNECT_DELAY_MS);
      reconnectAttemptRef.current = attempt + 1;
      clearReconnectTimer();
      reconnectTimerRef.current = window.setTimeout(() => {
        if (!cancelledRef.current) void connect();
      }, delay);
    };

    const connect = async () => {
      if (cancelledRef.current) return;
      let session;
      try {
        session = await auth.getSession();
      } catch {
        session = null;
      }
      if (cancelledRef.current) return;

      if (!session) {
        setError('Нет активной сессии');
        startPolling();
        return;
      }

      // No token in the query string any more. The session is an HttpOnly
      // cookie the browser attaches to the handshake by itself, which is also
      // why the server had to start checking Origin: a WebSocket handshake is
      // not subject to the same-origin policy, so a cookie alone would have let
      // any page the operator visits open this socket as them.
      const url = deriveWebSocketUrl(deployId, websocketUrl);
      let ws: WebSocket;
      try {
        ws = new WebSocket(url);
      } catch (err) {
        const message = err instanceof Error ? err.message : 'WebSocket connection failed';
        setError(message);
        startPolling();
        scheduleReconnect();
        return;
      }
      wsRef.current = ws;

      ws.onopen = () => {
        if (cancelledRef.current) return;
        reconnectAttemptRef.current = 0;
        setIsConnected(true);
        setError(null);
        stopPolling();
        if (!historyLoadedRef.current) {
          historyLoadedRef.current = true;
          void fetchHistory().finally(() => {
            if (!cancelledRef.current) setIsLoading(false);
          });
        } else {
          setIsLoading(false);
        }
      };

      ws.onmessage = (event) => {
        if (cancelledRef.current) return;
        try {
          const parsed = JSON.parse(event.data) as LogLine;
          ingest([parsed]);
        } catch {
          // ignore malformed payloads
        }
      };

      ws.onerror = () => {
        if (cancelledRef.current) return;
        setIsConnected(false);
      };

      ws.onclose = (event) => {
        if (cancelledRef.current) return;
        wsRef.current = null;
        setIsConnected(false);
        // 1000 = normal close, 1011 = deploy_failed (terminal); both end the stream.
        // 4001/4003 are unrecoverable auth/ownership errors.
        if ([1000, 1011, 4001, 4003].includes(event.code)) {
          stopPolling();
          setIsLoading(false);
          // Final history fetch to capture any tail lines after terminal close.
          void fetchHistory();
          return;
        }
        startPolling();
        scheduleReconnect();
      };
    };

    void connect();

    return () => {
      cancelledRef.current = true;
      clearReconnectTimer();
      stopPolling();
      const ws = wsRef.current;
      if (ws) {
        ws.onopen = null;
        ws.onmessage = null;
        ws.onerror = null;
        ws.onclose = null;
        if (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING) {
          ws.close();
        }
        wsRef.current = null;
      }
    };
  }, [deployId, websocketUrl]);

  return { logs, isConnected, isLoading, error };
}

export default useDeployLogs;
