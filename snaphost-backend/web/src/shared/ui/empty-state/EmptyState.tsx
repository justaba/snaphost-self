import styles from './EmptyState.module.css';

import type { ReactNode } from 'react';
import { Inbox } from 'lucide-react';

export interface EmptyStateProps {
  icon?: ReactNode;
  title: string;
  description?: string;
  action?: ReactNode;
  className?: string;
}

function EmptyState({ icon, title, description, action, className = '' }: EmptyStateProps) {
  return (
    <div
      className={[
        'flex flex-col items-center justify-center text-center px-6 py-16',
        'bg-white border border-dashed border-zinc-200 rounded-xl',
        className,
      ].join(' ')}
    >
      <div className={`${styles.root} mb-4 text-zinc-300`}>
        {icon ?? <Inbox className="w-16 h-16" strokeWidth={1.5} />}
      </div>
      <h3 className="text-base font-medium text-zinc-900 mb-1">{title}</h3>
      {description && <p className="text-sm text-zinc-500 max-w-md">{description}</p>}
      {action && <div className="mt-6">{action}</div>}
    </div>
  );
}

export default EmptyState;
