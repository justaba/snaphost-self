import { Cpu, MemoryStick, Network, HardDrive } from 'lucide-react';
import type { DeployDetail } from '@/entities/deploy';
import type { User } from '@/entities/session';
import styles from './ProjectResourcesTab.module.css';

export interface ProjectResourcesTabProps {
  deploy: DeployDetail;
  role?: User['role'];
}

interface RowProps {
  icon: React.ReactNode;
  label: string;
  value: string;
  hint?: string;
}

function Row({ icon, label, value, hint }: RowProps) {
  return (
    <div className="flex items-start justify-between gap-4 py-3 border-b border-zinc-100 last:border-b-0">
      <div className="flex items-center gap-2 text-sm text-zinc-700">
        <span className="text-zinc-400">{icon}</span>
        {label}
      </div>
      <div className="text-right">
        <div className="text-sm font-medium text-zinc-900">{value}</div>
        {hint && <div className="text-xs text-zinc-500">{hint}</div>}
      </div>
    </div>
  );
}

const TARIFF_LIMITS: Record<NonNullable<User['role']>, { cpu: string; ram: string }> = {
  user: { cpu: '0.5 ядра', ram: '512 МБ' },
  pro: { cpu: '2 ядра', ram: '2 ГБ' },
  admin: { cpu: '4 ядра', ram: '4 ГБ' },
};

function ProjectResourcesTab({ deploy, role = 'user' }: ProjectResourcesTabProps) {
  const limits = TARIFF_LIMITS[role];

  // TODO: hook up when /api/v1/deploys/:id/metrics endpoint is added
  return (
    <div className={styles.root}>
      <Row icon={<Cpu size={14} />} label="CPU лимит" value={limits.cpu} hint="по тарифу" />
      <Row icon={<MemoryStick size={14} />} label="RAM лимит" value={limits.ram} hint="по тарифу" />
      <Row
        icon={<HardDrive size={14} />}
        label="Образ"
        value={deploy.image_ref ? deploy.image_ref.split('/').slice(-1)[0] : '—'}
        hint={deploy.image_ref ?? undefined}
      />
      <Row
        icon={<Network size={14} />}
        label="Поддомен"
        value={deploy.subdomain ?? '—'}
        hint={deploy.endpoint_url ?? undefined}
      />
    </div>
  );
}

export default ProjectResourcesTab;
