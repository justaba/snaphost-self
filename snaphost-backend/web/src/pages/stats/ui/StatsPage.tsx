import styles from './StatsPage.module.css';

import { BarChart3 } from 'lucide-react';
import { EmptyState } from '@/shared/ui/empty-state';

function StatsPage() {
  return (
    <div className={`${styles.root} max-w-7xl mx-auto`}>
      <header className="mb-6">
        <h1 className="text-2xl font-semibold text-zinc-900 tracking-tight">Статистика</h1>
        <p className="text-sm text-zinc-500 mt-1">Метрики и аналитика по вашим проектам</p>
      </header>
      <EmptyState
        icon={<BarChart3 className="w-16 h-16" strokeWidth={1.5} />}
        title="Скоро здесь появится статистика"
        description="Мы готовим красивые графики использования ресурсов и трафика."
      />
    </div>
  );
}

export default StatsPage;
