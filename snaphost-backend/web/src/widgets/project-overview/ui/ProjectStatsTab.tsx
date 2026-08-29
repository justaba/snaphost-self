import { Activity, Clock, Coins, Timer } from 'lucide-react';
import { useUptime, type DeployDetail } from '@/entities/deploy';
import styles from './ProjectStatsTab.module.css';

export interface ProjectStatsTabProps {
  deploy: DeployDetail;
}

interface StatCardProps {
  icon: React.ReactNode;
  label: string;
  value: React.ReactNode;
  hint?: React.ReactNode;
}

function StatCard({ icon, label, value, hint }: StatCardProps) {
  return (
    <div className="rounded-lg border border-zinc-200 bg-white p-4">
      <div className="flex items-center gap-2 text-xs text-zinc-500 mb-2">
        <span className="text-zinc-400">{icon}</span>
        {label}
      </div>
      <div className="text-xl font-medium text-zinc-900 tabular-nums">{value}</div>
      {hint && <div className="text-xs text-zinc-500 mt-1">{hint}</div>}
    </div>
  );
}

function formatTtl(ttlIso: string | null): string {
  if (!ttlIso) return '—';
  const ttl = Date.parse(ttlIso);
  if (Number.isNaN(ttl)) return '—';
  const remaining = ttl - Date.now();
  if (remaining <= 0) return 'истёк';
  const minutes = Math.floor(remaining / 60_000);
  if (minutes < 60) return `${minutes}м`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}ч ${minutes % 60}м`;
  const days = Math.floor(hours / 24);
  return `${days}д ${hours % 24}ч`;
}

function ProjectStatsTab({ deploy }: ProjectStatsTabProps) {
  const { uptime } = useUptime(deploy.created_at, deploy.status, deploy.stopped_at);
  return (
    <div className={styles.root}>
      <StatCard
        icon={<Clock size={14} />}
        label="Время работы"
        value={uptime}
        hint={`Создан ${new Date(deploy.created_at).toLocaleString()}`}
      />
      <StatCard
        icon={<Activity size={14} />}
        label="Запросов"
        value="—"
        hint="Будет доступно позже"
      />
      <StatCard
        icon={<Coins size={14} />}
        label="Стоимость"
        value={`${deploy.cost_vibecoins}`}
        hint="вайб-коинов"
      />
      <StatCard
        icon={<Timer size={14} />}
        label="TTL"
        value={formatTtl(deploy.ttl_expires_at)}
        hint={
          deploy.ttl_expires_at ? new Date(deploy.ttl_expires_at).toLocaleString() : 'без срока'
        }
      />
    </div>
  );
}

export default ProjectStatsTab;
