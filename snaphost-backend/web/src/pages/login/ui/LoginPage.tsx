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
        <div className="mb-6 text-center">
          <h1 className="text-2xl font-semibold tracking-tight text-zinc-900">SnapHost</h1>
          <p className="mt-1 text-sm text-zinc-500">Войдите, чтобы управлять этим сервером.</p>
        </div>

        {/* A card, so the form sits on a surface rather than on the page
            background. Same border, radius and padding as every other panel
            surface — this screen used to be the one place that looked like it
            belonged to a different application. */}
        <div className="rounded-xl border border-zinc-200 bg-white p-6 shadow-sm">
          <LoginForm />
        </div>

        {status === 'loading' && (
          <p className="mt-4 text-center text-sm text-zinc-500" aria-live="polite">
            Проверяем активную сессию…
          </p>
        )}

        <p className="mt-6 text-center text-xs text-zinc-400">
          Пароль оператора печатается один раз при первом запуске и не восстанавливается.
        </p>
      </div>
    </div>
  );
}
