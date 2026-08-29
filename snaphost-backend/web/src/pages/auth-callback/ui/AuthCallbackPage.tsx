import styles from './AuthCallbackPage.module.css';

import { useEffect, type ReactElement } from 'react';
import { useNavigate } from 'react-router-dom';

import { auth } from '@/entities/session';

export default function AuthCallbackPage(): ReactElement {
  const navigate = useNavigate();

  useEffect(() => {
    let cancelled = false;
    const timeout = window.setTimeout(() => {
      if (!cancelled) {
        navigate('/login?error=callback_failed', { replace: true });
      }
    }, 2000);

    auth
      .getSession()
      .then((session) => {
        if (cancelled) return;
        if (session) {
          window.clearTimeout(timeout);
          navigate('/dashboard', { replace: true });
        }
      })
      .catch(() => {
        // Swallow; the 2s fallback will redirect to login with an error flag.
      });

    return () => {
      cancelled = true;
      window.clearTimeout(timeout);
    };
  }, [navigate]);

  return (
    <div
      className={`${styles.root} min-h-screen flex flex-col items-center justify-center bg-slate-50 gap-4`}
    >
      <div className="animate-spin w-10 h-10 border-4 border-violet-500 border-t-transparent rounded-full" />
      <p className="text-slate-600 font-medium">Signing you in…</p>
    </div>
  );
}
