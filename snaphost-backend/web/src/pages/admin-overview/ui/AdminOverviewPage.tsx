import styles from './AdminOverviewPage.module.css';

import { Link } from 'react-router-dom';
import { Globe, KeyRound, Rocket, Users } from 'lucide-react';

import { formatCount, useAdminOverview } from '@/entities/admin';
import { Skeleton } from '@/shared/ui/skeleton';
import { StatTile } from '@/shared/ui/stat-tile';

function AdminOverviewPage() {
  const { data, isLoading, isError } = useAdminOverview();

  if (isError) {
    return (
      <p
        className={`${styles.root} text-sm text-red-600 bg-red-50 border border-red-200 rounded-lg px-4 py-3`}
      >
        Не удалось загрузить сводку. Обновите страницу.
      </p>
    );
  }

  if (isLoading || !data) {
    return (
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
        {Array.from({ length: 8 }, (_, i) => (
          <Skeleton key={i} className="h-24 w-full" />
        ))}
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-6">
      <section className="grid grid-cols-2 lg:grid-cols-4 gap-3">
        <StatTile
          label="Пользователи"
          value={data.users}
          icon={<Users size={14} />}
          hint={`${formatCount(data.projects)} проектов`}
        />
        <StatTile
          label="Деплои"
          value={data.deploys}
          icon={<Rocket size={14} />}
          hint={`${formatCount(data.deploys_running)} запущено · ${formatCount(data.deploys_failed)} с ошибкой`}
        />
        <StatTile
          label="Деплоев за сутки"
          value={data.deploys_24h}
          icon={<Rocket size={14} />}
          hint={`${formatCount(data.projects)} проектов всего`}
        />
        <StatTile
          label="Домены подтверждены"
          value={data.domains_verified}
          icon={<Globe size={14} />}
          hint={`${formatCount(data.domains_pending)} ждут DNS`}
        />
        <StatTile
          label="Активные API-ключи"
          value={data.active_api_keys}
          icon={<KeyRound size={14} />}
        />
      </section>

      <nav className="flex flex-wrap gap-2 text-sm">
        <Link
          to="/dashboard/admin/users"
          className="px-3 py-2 rounded-lg border border-zinc-200 bg-white hover:bg-zinc-50 text-zinc-700"
        >
          Пользователи →
        </Link>
        <Link
          to="/dashboard/admin/deploys"
          className="px-3 py-2 rounded-lg border border-zinc-200 bg-white hover:bg-zinc-50 text-zinc-700"
        >
          Деплои →
        </Link>
        <Link
          to="/dashboard/admin/transactions"
          className="px-3 py-2 rounded-lg border border-zinc-200 bg-white hover:bg-zinc-50 text-zinc-700"
        >
          Транзакции →
        </Link>
        <Link
          to="/dashboard/admin/domains"
          className="px-3 py-2 rounded-lg border border-zinc-200 bg-white hover:bg-zinc-50 text-zinc-700"
        >
          Домены →
        </Link>
      </nav>
    </div>
  );
}

export default AdminOverviewPage;
