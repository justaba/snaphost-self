import { Network, HardDrive } from 'lucide-react';
import type { DeployDetail } from '@/entities/deploy';
import styles from './ProjectResourcesTab.module.css';

export interface ProjectResourcesTabProps {
  deploy: DeployDetail;
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

function ProjectResourcesTab({ deploy }: ProjectResourcesTabProps) {
  // The CPU and RAM rows used to read from a table keyed by the account's
  // role — 0.5 cores for a free user, 2 for pro, 4 for an admin. That is a
  // pricing tier, and this platform has none: every container gets
  // CONTAINER_CPU_LIMIT and CONTAINER_MEMORY_MB from the operator's own
  // configuration. Showing an invented number would be worse than showing
  // none, so the rows are gone until there is somewhere to read the real one.
  //
  // TODO: hook up when /api/v1/deploys/:id/metrics endpoint is added
  return (
    <div className={styles.root}>
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
