import styles from './Tabs.module.css';

import { useId, useRef, type KeyboardEvent, type ReactNode } from 'react';

export interface TabItem {
  value: string;
  label: ReactNode;
  icon?: ReactNode;
  disabled?: boolean;
}

export interface TabsProps {
  items: TabItem[];
  value: string;
  onChange: (value: string) => void;
  className?: string;
  ariaLabel?: string;
}

function Tabs({ items, value, onChange, className = '', ariaLabel }: TabsProps) {
  const baseId = useId();
  const listRef = useRef<HTMLDivElement | null>(null);

  const handleKeyDown = (e: KeyboardEvent<HTMLButtonElement>, index: number) => {
    if (e.key !== 'ArrowRight' && e.key !== 'ArrowLeft' && e.key !== 'Home' && e.key !== 'End') {
      return;
    }
    e.preventDefault();
    const enabled = items.map((item, i) => ({ item, i })).filter(({ item }) => !item.disabled);
    if (enabled.length === 0) return;
    const currentEnabledIndex = enabled.findIndex(({ i }) => i === index);
    let nextIndex = currentEnabledIndex;
    if (e.key === 'ArrowRight') nextIndex = (currentEnabledIndex + 1) % enabled.length;
    if (e.key === 'ArrowLeft')
      nextIndex = (currentEnabledIndex - 1 + enabled.length) % enabled.length;
    if (e.key === 'Home') nextIndex = 0;
    if (e.key === 'End') nextIndex = enabled.length - 1;
    const target = enabled[nextIndex];
    onChange(target.item.value);
    const buttons = listRef.current?.querySelectorAll<HTMLButtonElement>('[role="tab"]');
    buttons?.[target.i]?.focus();
  };

  return (
    <div
      ref={listRef}
      role="tablist"
      aria-label={ariaLabel}
      className={`inline-flex items-center gap-1 p-1 bg-zinc-100 rounded-lg ${className}`}
    >
      {items.map((item, index) => {
        const selected = item.value === value;
        return (
          <button
            key={item.value}
            id={`${baseId}-tab-${item.value}`}
            role="tab"
            type="button"
            aria-selected={selected}
            aria-controls={`${baseId}-panel-${item.value}`}
            tabIndex={selected ? 0 : -1}
            disabled={item.disabled}
            onClick={() => onChange(item.value)}
            onKeyDown={(e) => handleKeyDown(e, index)}
            className={[
              'inline-flex items-center gap-1.5 h-8 px-3 rounded-md text-sm font-medium',
              'transition-all duration-150',
              'focus:outline-none focus-visible:ring-2 focus-visible:ring-zinc-300',
              'disabled:opacity-50 disabled:cursor-not-allowed',
              selected ? 'bg-white text-zinc-900 shadow-sm' : 'text-zinc-600 hover:text-zinc-900',
            ].join(' ')}
          >
            {item.icon && <span className={`${styles.root} inline-flex`}>{item.icon}</span>}
            {item.label}
          </button>
        );
      })}
    </div>
  );
}

export default Tabs;
