import { useEffect, useMemo, useState } from 'react';
import type { DeployStatus } from './types';

const TICK_INTERVAL_MS = 30_000;

function diffParts(fromMs: number, toMs: number) {
  const totalSeconds = Math.max(0, Math.floor((toMs - fromMs) / 1000));
  const days = Math.floor(totalSeconds / 86_400);
  const hours = Math.floor((totalSeconds % 86_400) / 3_600);
  const minutes = Math.floor((totalSeconds % 3_600) / 60);
  return { days, hours, minutes, totalSeconds };
}

function formatUptime(fromIso: string, toIso: string | null): string {
  const fromMs = Date.parse(fromIso);
  const toMs = toIso ? Date.parse(toIso) : Date.now();
  if (Number.isNaN(fromMs) || Number.isNaN(toMs)) return '—';
  const { days, hours, minutes } = diffParts(fromMs, toMs);

  if (days > 0) return `${days}д ${hours}ч`;
  if (hours > 0) return `${hours}ч ${minutes}м`;
  if (minutes > 0) return `${minutes}м`;
  return '<1м';
}

export interface UseUptimeResult {
  uptime: string;
  isLive: boolean;
}

export function useUptime(
  createdAt: string,
  status: DeployStatus,
  stoppedAt?: string | null,
): UseUptimeResult {
  const isLive = status === 'running';
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    if (!isLive) return;
    const id = window.setInterval(() => setNow(Date.now()), TICK_INTERVAL_MS);
    return () => window.clearInterval(id);
  }, [isLive]);

  const uptime = useMemo(() => {
    if (isLive) {
      return formatUptime(createdAt, new Date(now).toISOString());
    }
    return formatUptime(createdAt, stoppedAt ?? null);
  }, [createdAt, stoppedAt, isLive, now]);

  return { uptime, isLive };
}

export default useUptime;
