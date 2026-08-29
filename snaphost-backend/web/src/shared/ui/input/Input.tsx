import styles from './Input.module.css';

import { forwardRef, useId, type InputHTMLAttributes, type ReactNode } from 'react';

export interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  label?: ReactNode;
  hint?: ReactNode;
  error?: ReactNode;
  iconLeft?: ReactNode;
}

const Input = forwardRef<HTMLInputElement, InputProps>(function Input(
  { label, hint, error, iconLeft, id, className = '', ...rest },
  ref,
) {
  const reactId = useId();
  const inputId = id ?? reactId;
  const describedBy: string[] = [];
  if (hint) describedBy.push(`${inputId}-hint`);
  if (error) describedBy.push(`${inputId}-error`);

  return (
    <div className={`${styles.root} flex flex-col gap-1.5`}>
      {label && (
        <label htmlFor={inputId} className="text-sm font-medium text-zinc-700">
          {label}
        </label>
      )}
      <div className="relative">
        {iconLeft && (
          <span className="absolute left-3 top-1/2 -translate-y-1/2 text-zinc-400 pointer-events-none">
            {iconLeft}
          </span>
        )}
        <input
          ref={ref}
          id={inputId}
          aria-invalid={error ? true : undefined}
          aria-describedby={describedBy.length > 0 ? describedBy.join(' ') : undefined}
          className={[
            'w-full bg-white border rounded-md text-sm text-zinc-900 placeholder:text-zinc-400',
            'transition-colors duration-150',
            'focus:outline-none focus-visible:ring-2 focus-visible:ring-zinc-300 focus-visible:border-zinc-400',
            'disabled:bg-zinc-50 disabled:cursor-not-allowed disabled:text-zinc-500',
            iconLeft ? 'pl-9 pr-3 py-2' : 'px-3 py-2',
            error ? 'border-red-400' : 'border-zinc-300',
            className,
          ].join(' ')}
          {...rest}
        />
      </div>
      {hint && !error && (
        <p id={`${inputId}-hint`} className="text-xs text-zinc-500">
          {hint}
        </p>
      )}
      {error && (
        <p id={`${inputId}-error`} className="text-xs text-red-600">
          {error}
        </p>
      )}
    </div>
  );
});

export default Input;
