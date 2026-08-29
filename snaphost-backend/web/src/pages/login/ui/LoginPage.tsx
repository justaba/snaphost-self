import styles from './LoginPage.module.css';

import { useEffect } from 'react';
import { Link, Navigate } from 'react-router-dom';

import { useAuth } from '@/entities/session';
import { LoginForm } from '@/features/sign-in';
import { AuthLayout } from '@/widgets/marketing-chrome';

export default function LoginPage() {
  const { isAuthenticated, status } = useAuth();

  useEffect(() => {
    document.title = 'Вход — Snaphost';
  }, []);

  if (isAuthenticated) {
    return <Navigate to="/dashboard" replace />;
  }

  return (
    <div className={styles.root}>
      <AuthLayout
        kicker="SECURE WORKSPACE"
        title={
          <>
            Вернитесь
            <br />к следующему
            <br />
            <span>релизу.</span>
          </>
        }
        description="Все проекты, деплои и логи сборки уже ждут в рабочем пространстве Snaphost."
        routeLabel="ACCOUNT FLOW / READY"
        routePoints={['Identity', 'Workspace', 'Deploy']}
        panelCode="SN / AUTH"
        step="01 / SIGN IN"
        panelId="login-title"
        panelTitle="Добро пожаловать"
        panelDescription="Войдите, чтобы продолжить работу с проектами."
        footerHref="/legal/privacy"
        footerLabel="Конфиденциальность"
      >
        <LoginForm />
        <p className="auth-register">
          Нет аккаунта? <Link to="/signup">Создать бесплатно</Link>
        </p>
        {status === 'loading' && (
          <p className="auth-session-state" aria-live="polite">
            Проверяем активную сессию…
          </p>
        )}
      </AuthLayout>
    </div>
  );
}
