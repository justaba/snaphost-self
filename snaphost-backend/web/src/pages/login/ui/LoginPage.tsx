import styles from './LoginPage.module.css';

import { useEffect, useState } from 'react';
import { Navigate } from 'react-router-dom';

import { auth, useAuth } from '@/entities/session';
import { LoginForm } from '@/features/sign-in';
import { SetupForm } from '@/features/operator-setup';

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
  const [required, setRequired] = useState<boolean | null>(null);
  const [setupError, setSetupError] = useState(false);
  const [token] = useState(
    () => new URLSearchParams(window.location.hash.slice(1)).get('setup-token') ?? '',
  );

  useEffect(() => {
    // Fragments never reach the server. Remove the secret from the current
    // browser URL before any navigation; keep it only in this component.
    if (window.location.hash.includes('setup-token=')) {
      window.history.replaceState(
        window.history.state,
        '',
        window.location.pathname + window.location.search,
      );
    }
    let active = true;
    auth
      .setupRequired()
      .then((value) => {
        if (active) setRequired(value);
      })
      .catch(() => {
        if (active) setSetupError(true);
      });
    return () => {
      active = false;
    };
  }, []);

  useEffect(() => {
    document.title = required ? 'Настройка — SnapHost' : 'Вход — SnapHost';
  }, [required]);

  if (isAuthenticated) {
    return <Navigate to="/dashboard" replace />;
  }

  return (
    <div className={`${styles.root} flex min-h-screen items-center justify-center bg-zinc-50 p-6`}>
      <div className="w-full max-w-sm">
        <div className="mb-6 text-center">
          <h1 className="text-2xl font-semibold tracking-tight text-zinc-900">SnapHost</h1>
          <p className="mt-1 text-sm text-zinc-500">
            {required ? 'Создайте аккаунт оператора.' : 'Войдите, чтобы управлять этим сервером.'}
          </p>
        </div>

        {/* A card, so the form sits on a surface rather than on the page
            background. Same border, radius and padding as every other panel
            surface — this screen used to be the one place that looked like it
            belonged to a different application. */}
        <div className="rounded-xl border border-zinc-200 bg-white p-6 shadow-sm">
          {setupError ? (
            <p role="alert" className="text-sm text-red-700">
              Не удалось проверить настройку. Обновите страницу.
            </p>
          ) : required === null ? (
            <p role="status" className="text-sm text-zinc-500">
              Проверяем настройку…
            </p>
          ) : required ? (
            <SetupForm token={token} />
          ) : (
            <LoginForm />
          )}
        </div>

        {status === 'loading' && (
          <p className="mt-4 text-center text-sm text-zinc-500" aria-live="polite">
            Проверяем активную сессию…
          </p>
        )}

        <p className="mt-6 text-center text-xs text-zinc-400">
          {required
            ? 'Выберите логин и пароль, которые будете использовать для входа.'
            : 'Используйте логин и пароль, заданные при первоначальной настройке.'}
        </p>
      </div>
    </div>
  );
}
