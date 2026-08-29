import styles from './Spinner.module.css';

import type { CSSProperties } from 'react';

export interface SpinnerProps {
  size?: 'sm' | 'md' | 'lg';
  className?: string;
}

const SIZE_MAP: Record<NonNullable<SpinnerProps['size']>, string> = {
  sm: 'w-4 h-4 border-2',
  md: 'w-6 h-6 border-2',
  lg: 'w-10 h-10 border-[3px]',
};

function Spinner({ size = 'md', className = '' }: SpinnerProps) {
  const style: CSSProperties = { borderTopColor: 'transparent' };
  return (
    <span
      role="status"
      aria-label="Loading"
      className={`${styles.root} inline-block rounded-full border-current animate-spin ${SIZE_MAP[size]} ${className}`}
      style={style}
    />
  );
}

export default Spinner;
