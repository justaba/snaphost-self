import './AuthProvider.module.css';

import { useEffect, type ReactNode } from 'react';

import { bootstrapAuth, sessionChanged, useSessionDispatch } from '@/entities/session';
import { configureApiAuth } from '@/shared/api/http';

interface AuthProviderProps {
  children: ReactNode;
}

export function AuthProvider({ children }: AuthProviderProps) {
  const dispatch = useSessionDispatch();

  useEffect(() => {
    // The API client cannot see the session cookie, so it cannot tell whether
    // one exists — only the server can, by refusing. A 401 from any request is
    // therefore the signal that the session ended, and this puts the store back
    // in step with that rather than leaving the panel rendering data it can no
    // longer fetch.
    configureApiAuth({
      onUnauthenticated: () => {
        dispatch(sessionChanged(null));
      },
    });

    // Asking who we are is the only way to find out: the cookie is HttpOnly.
    // The listener that used to be here existed because Supabase could change
    // the session in another tab; nothing does that now.
    void dispatch(bootstrapAuth());
  }, [dispatch]);

  return <>{children}</>;
}
