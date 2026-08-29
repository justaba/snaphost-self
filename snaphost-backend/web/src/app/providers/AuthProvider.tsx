import './AuthProvider.module.css';

import { useEffect, type ReactNode } from 'react';

import { auth, bootstrapAuth, sessionChanged, useSessionDispatch } from '@/entities/session';
import { configureApiAuth } from '@/shared/api/http';

configureApiAuth(auth);

interface AuthProviderProps {
  children: ReactNode;
}

export function AuthProvider({ children }: AuthProviderProps) {
  const dispatch = useSessionDispatch();

  useEffect(() => {
    void dispatch(bootstrapAuth());
    const unsubscribe = auth.onAuthStateChange((session) => {
      dispatch(sessionChanged(session));
    });
    return () => {
      unsubscribe();
    };
  }, [dispatch]);

  return <>{children}</>;
}
