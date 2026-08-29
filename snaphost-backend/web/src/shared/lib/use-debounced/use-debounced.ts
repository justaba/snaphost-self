import { useEffect, useState } from 'react';

/** Delays a fast-changing value (a search box) so every keystroke does not
 *  become a request. The immediate value still drives the input; only the
 *  query key follows this one. */
export function useDebounced<T>(value: T, delayMs = 300): T {
  const [debounced, setDebounced] = useState(value);

  useEffect(() => {
    const timer = window.setTimeout(() => setDebounced(value), delayMs);
    return () => window.clearTimeout(timer);
  }, [value, delayMs]);

  return debounced;
}
