import styles from './DeployStatusBadge.module.css';

import {
  CheckCircle2,
  Clock,
  Hammer,
  Loader2,
  XCircle,
  PauseCircle,
  Trash2,
  type LucideIcon,
} from 'lucide-react';
import type { DeployStatus } from '../model/types';

export interface DeployStatusBadgeProps {
  status: DeployStatus;
  size?: 'sm' | 'md';
}

interface BadgeStyle {
  label: string;
  classes: string;
  Icon: LucideIcon;
  spin?: boolean;
}

const STYLES: Record<DeployStatus, BadgeStyle> = {
  running: {
    label: 'Запущен',
    classes: 'bg-emerald-50 text-emerald-700 border-emerald-200',
    Icon: CheckCircle2,
  },
  pending: {
    label: 'В очереди',
    classes: 'bg-zinc-100 text-zinc-700 border-zinc-200',
    Icon: Clock,
  },
  reserved: {
    label: 'В очереди',
    classes: 'bg-zinc-100 text-zinc-700 border-zinc-200',
    Icon: Clock,
  },
  building: {
    label: 'Сборка',
    classes: 'bg-amber-50 text-amber-700 border-amber-200',
    Icon: Hammer,
  },
  built: {
    label: 'Сборка',
    classes: 'bg-amber-50 text-amber-700 border-amber-200',
    Icon: Hammer,
  },
  provisioning: {
    label: 'Запуск',
    classes: 'bg-amber-50 text-amber-700 border-amber-200',
    Icon: Loader2,
    spin: true,
  },
  failed: {
    label: 'Ошибка',
    classes: 'bg-red-50 text-red-700 border-red-200',
    Icon: XCircle,
  },
  stopped: {
    label: 'Остановлен',
    classes: 'bg-zinc-100 text-zinc-700 border-zinc-200',
    Icon: PauseCircle,
  },
  deleted: {
    label: 'Удалён',
    classes: 'bg-zinc-100 text-zinc-500 border-zinc-200',
    Icon: Trash2,
  },
};

function DeployStatusBadge({ status, size = 'md' }: DeployStatusBadgeProps) {
  const style = STYLES[status];
  const Icon = style.Icon;
  const sizeClass = size === 'sm' ? 'h-6 px-2 text-xs gap-1' : 'h-7 px-2.5 text-xs gap-1.5';
  const iconSize = size === 'sm' ? 12 : 14;

  return (
    <span
      className={[
        styles.root,
        'inline-flex items-center rounded-full border font-medium',
        sizeClass,
        style.classes,
      ].join(' ')}
    >
      <Icon size={iconSize} className={style.spin ? 'animate-spin' : ''} />
      {style.label}
    </span>
  );
}

export default DeployStatusBadge;
