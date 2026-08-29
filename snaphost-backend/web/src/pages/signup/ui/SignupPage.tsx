import styles from './SignupPage.module.css';

import { useEffect } from 'react';
import { Link, Navigate } from 'react-router-dom';

import { useAuth } from '@/entities/session';
import { SignupForm } from '@/features/sign-up';
import { AuthLayout } from '@/widgets/marketing-chrome';

export default function SignupPage() {
  const { isAuthenticated } = useAuth();

  useEffect(() => {
    document.title = 'Регистрация — Snaphost';
  }, []);

  if (isAuthenticated) {
    return <Navigate to="/dashboard" replace />;
  }

  return (
    <div className={styles.root}>
      <AuthLayout
        variant="signup"
        kicker="START BUILDING"
        title={
          <>
            Один аккаунт.
            <br />
            Любой проект.
            <br />
            <span>Сразу в сеть.</span>
          </>
        }
        description="Создайте рабочее пространство и превратите первый GitHub-репозиторий в готовое приложение."
        routeLabel="ACCOUNT FLOW / READY"
        routePoints={['Account', 'Repository', 'Production']}
        panelCode="SN / AUTH"
        step="02 / CREATE ACCOUNT"
        panelId="signup-title"
        panelTitle="Создать аккаунт"
        panelDescription="Бесплатный тариф активируется сразу. Карта не нужна."
        footerHref="/legal/offer"
        footerLabel="Условия сервиса"
      >
        <SignupForm />
        <p className="auth-register">
          Уже есть аккаунт? <Link to="/login">Войти</Link>
        </p>
      </AuthLayout>
    </div>
  );
}
