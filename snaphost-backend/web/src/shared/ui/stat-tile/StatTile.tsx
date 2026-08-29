import styles from './StatTile.module.css';

import type { ReactNode } from 'react';

import { formatCompact } from '@/shared/lib/format-number';

export interface StatTileProps {
  /** Sentence case, no trailing colon. */
  label: string;
  value: number;
  /** Secondary line under the value — a breakdown, not a second headline. */
  hint?: ReactNode;
  /** Set when the tile reports a condition rather than a quantity. It carries
   *  an icon and words as well as color, so the state never reads by hue
   *  alone. */
  alert?: boolean;
  icon?: ReactNode;
}

/** One headline number. The number is the chart here: a single current value
 *  belongs in a tile, not in a one-bar bar chart.
 *
 *  Ink stays in the text tokens — zinc-900 for the figure, zinc-500 for the
 *  label — so nothing on this row competes with the one tile that turns amber
 *  when it has something to report. */
function StatTile({ label, value, hint, alert = false, icon }: StatTileProps) {
  const active = alert && value > 0;

  return (
    <div
      className={[
        'bg-white border rounded-xl px-4 py-3.5 flex flex-col gap-1',
        active ? 'border-amber-300 bg-amber-50' : 'border-zinc-200',
      ].join(' ')}
    >
      <div className={`${styles.root} flex items-center gap-1.5 text-xs text-zinc-500`}>
        {icon && <span className={active ? 'text-amber-600' : 'text-zinc-400'}>{icon}</span>}
        <span>{label}</span>
      </div>
      <div
        className={[
          'text-2xl font-semibold tabular-nums tracking-tight',
          active ? 'text-amber-800' : 'text-zinc-900',
        ].join(' ')}
        title={value.toLocaleString('ru-RU')}
      >
        {formatCompact(value)}
      </div>
      {hint && <div className="text-xs text-zinc-500">{hint}</div>}
    </div>
  );
}

export default StatTile;
