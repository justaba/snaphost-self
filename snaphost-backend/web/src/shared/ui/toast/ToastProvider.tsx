import styles from './ToastProvider.module.css';

import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { CheckCircle2, AlertCircle, Info, AlertTriangle, X } from 'lucide-react';

import { ToastContext, type ToastContextValue, type ToastItem, type ToastType } from './model';

const TYPE_STYLES: Record<ToastType, { icon: ReactNode; classes: string }> = {
  success: {
    icon: <CheckCircle2 size={18} />,
    classes: 'bg-emerald-50 text-emerald-800 border-emerald-200',
  },
  error: {
    icon: <AlertCircle size={18} />,
    classes: 'bg-red-50 text-red-800 border-red-200',
  },
  info: {
    icon: <Info size={18} />,
    classes: 'bg-zinc-50 text-zinc-800 border-zinc-200',
  },
  warn: {
    icon: <AlertTriangle size={18} />,
    classes: 'bg-amber-50 text-amber-800 border-amber-200',
  },
};

export interface ToastProviderProps {
  children: ReactNode;
}

export function ToastProvider({ children }: ToastProviderProps) {
  const [items, setItems] = useState<ToastItem[]>([]);
  const timers = useRef<Map<string, number>>(new Map());

  const dismiss = useCallback((id: string) => {
    setItems((prev) => prev.filter((t) => t.id !== id));
    const timer = timers.current.get(id);
    if (timer) {
      window.clearTimeout(timer);
      timers.current.delete(id);
    }
  }, []);

  const toast = useCallback<ToastContextValue['toast']>(
    (message, type = 'info', duration = 4000) => {
      const id =
        typeof crypto !== 'undefined' && 'randomUUID' in crypto
          ? crypto.randomUUID()
          : `toast-${Date.now()}-${Math.random()}`;
      const item: ToastItem = { id, message, type, duration };
      setItems((prev) => [...prev, item]);
      const timer = window.setTimeout(() => dismiss(id), duration);
      timers.current.set(id, timer);
    },
    [dismiss],
  );

  useEffect(() => {
    const timersSnapshot = timers.current;
    return () => {
      timersSnapshot.forEach((t) => window.clearTimeout(t));
      timersSnapshot.clear();
    };
  }, []);

  const value = useMemo(() => ({ toast }), [toast]);

  return (
    <ToastContext.Provider value={value}>
      {children}
      {createPortal(
        <div
          aria-live="polite"
          aria-atomic="true"
          className={`${styles.root} fixed top-4 right-4 z-[100] flex flex-col gap-2 pointer-events-none`}
        >
          {items.map((item) => {
            const style = TYPE_STYLES[item.type];
            return (
              <div
                key={item.id}
                role="status"
                className={[
                  'pointer-events-auto flex items-start gap-3 min-w-[280px] max-w-md',
                  'px-4 py-3 border rounded-lg shadow-sm bg-white',
                  'transition-all duration-150',
                  style.classes,
                ].join(' ')}
              >
                <span className="shrink-0 mt-0.5">{style.icon}</span>
                <div className="flex-1 text-sm">{item.message}</div>
                <button
                  type="button"
                  onClick={() => dismiss(item.id)}
                  aria-label="Закрыть уведомление"
                  className="shrink-0 p-0.5 rounded hover:bg-black/5 transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-zinc-400"
                >
                  <X size={14} />
                </button>
              </div>
            );
          })}
        </div>,
        document.body,
      )}
    </ToastContext.Provider>
  );
}

export default ToastProvider;
