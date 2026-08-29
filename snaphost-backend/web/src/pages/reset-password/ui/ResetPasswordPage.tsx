import styles from './ResetPasswordPage.module.css';

import { useEffect } from 'react';

import { ResetPasswordForm } from '@/features/reset-password';
import { AuthLayout } from '@/widgets/marketing-chrome';

export default function ResetPasswordPage() {
  useEffect(() => {
    document.title = 'Новый пароль — Snaphost';
  }, []);

  return (
    <div className={styles.root}>
      <AuthLayout
        variant="recover"
        kicker="ACCOUNT RECOVERY"
        title={
          <>
            Новый пароль.
            <br />
            Тот же
            <br />
            <span>workspace.</span>
          </>
        }
        description="Задайте новый пароль и вернитесь к своим проектам без потери данных."
        routeLabel="RECOVERY / PASSWORD"
        routePoints={['Verified', 'New password', 'Workspace']}
        panelCode="SN / RESET"
        step="04 / NEW PASSWORD"
        panelId="reset-password-title"
        panelTitle="Задайте пароль"
        panelDescription="Используйте не менее восьми символов."
        footerHref="/recover"
        footerLabel="Запросить новую ссылку"
      >
        <ResetPasswordForm />
      </AuthLayout>
    </div>
  );
}
