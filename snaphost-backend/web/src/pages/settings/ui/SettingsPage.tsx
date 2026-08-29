import styles from './SettingsPage.module.css';

import { Settings } from 'lucide-react';
import { EmptyState } from '@/shared/ui/empty-state';

function SettingsPage() {
  return (
    <div className={`${styles.root} max-w-7xl mx-auto`}>
      <header className="mb-6">
        <h1 className="text-2xl font-semibold text-zinc-900 tracking-tight">Настройки</h1>
        <p className="text-sm text-zinc-500 mt-1">Профиль, биллинг, токены</p>
      </header>
      <EmptyState
        icon={<Settings className="w-16 h-16" strokeWidth={1.5} />}
        title="Скоро здесь появятся настройки"
        description="Подключение GitHub, управление биллингом и токенами доступа."
      />
    </div>
  );
}

export default SettingsPage;
