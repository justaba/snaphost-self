import styles from './RecoverPage.module.css';

import { useEffect } from 'react';

import { RecoveryForm } from '@/features/recover-access';
import { AuthLayout } from '@/widgets/marketing-chrome';

export default function RecoverPage() {
  useEffect(() => {
    document.title = 'Восстановление пароля — Snaphost';
  }, []);

  return (
    <div className={styles.root}>
      <AuthLayout
        variant="recover"
        kicker="ACCOUNT RECOVERY"
        title={
          <>
            Верните доступ.
            <br />
            Продолжайте
            <br />
            <span>сборку.</span>
          </>
        }
        description="Мы отправим защищённую ссылку для смены пароля. Ваши проекты и деплои останутся на месте."
        routeLabel="ACCOUNT FLOW / READY"
        routePoints={['Email', 'Secure link', 'New password']}
        panelCode="SN / AUTH"
        step="03 / RECOVER ACCESS"
        panelId="recover-title"
        panelTitle="Восстановить пароль"
        panelDescription="Укажите email, который использовали при регистрации."
        footerHref="/legal/privacy"
        footerLabel="Конфиденциальность"
      >
        <RecoveryForm />
      </AuthLayout>
    </div>
  );
}
