import styles from './StatusPill.module.css';

export type Tone = 'green' | 'amber' | 'red' | 'zinc' | 'blue';

const TONE_CLASSES: Record<Tone, string> = {
  green: 'bg-emerald-50 text-emerald-700 border-emerald-200',
  amber: 'bg-amber-50 text-amber-700 border-amber-200',
  red: 'bg-red-50 text-red-700 border-red-200',
  blue: 'bg-blue-50 text-blue-700 border-blue-200',
  zinc: 'bg-zinc-100 text-zinc-600 border-zinc-200',
};

export interface StatusPillProps {
  tone: Tone;
  children: React.ReactNode;
  title?: string;
}

function StatusPill({ tone, children, title }: StatusPillProps) {
  return (
    <span
      title={title}
      className={`${styles.root} inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium whitespace-nowrap ${TONE_CLASSES[tone]}`}
    >
      {children}
    </span>
  );
}

export default StatusPill;
