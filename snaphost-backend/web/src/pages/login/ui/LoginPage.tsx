import styles from './LoginPage.module.css';

import { useEffect } from 'react';
import { Navigate } from 'react-router-dom';

import { useAuth } from '@/entities/session';
import { LoginForm } from '@/features/sign-in';

/**
 * The sign-in screen, without the marketing chrome it used to be wrapped in.
 *
 * AuthLayout took a kicker, a headline, a route diagram and a privacy-policy
 * footer, and sat beside a "no account? create one free" link. Every one of
 * those addresses somebody deciding whether to buy a service. The person
 * reading this screen installed it.
 */
export default function LoginPage() {
  const { isAuthenticated, status } = useAuth();

  useEffect(() => {
    document.title = 'Вход — Snaphost';
  }, []);

  if (isAuthenticated) {
    return <Navigate to="/dashboard" replace />;
  }

  return (
    <div className={`${styles.root} flex min-h-screen items-center justify-center bg-zinc-50 p-6`}>
      <div className="w-full max-w-sm">
        <h1 className="mb-1 text-xl font-semibold tracking-tight text-zinc-900">Snaphost</h1>
        <p className="mb-6 text-sm text-zinc-500">Войдите, чтобы управлять этим сервером.</p>
        <LoginForm />
        {status === 'loading' && (
          <p className="mt-4 text-sm text-zinc-500" aria-live="polite">
            Проверяем активную сессию…
          </p>
        )}
      </div>
    </div>
  );
}
