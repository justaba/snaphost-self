import styles from './AdminOverviewPage.module.css';

import { Link } from 'react-router-dom';
import { AlertTriangle, Globe, KeyRound, Rocket, Users, Wallet } from 'lucide-react';

import { formatCoins, useAdminOverview } from '@/entities/admin';
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
          hint={`${formatCoins(data.wallets)} с кошельком`}
        />
        <StatTile
          label="Деплои"
          value={data.deploys}
          icon={<Rocket size={14} />}
          hint={`${formatCoins(data.deploys_running)} запущено · ${formatCoins(data.deploys_failed)} с ошибкой`}
        />
        <StatTile
          label="Деплоев за сутки"
          value={data.deploys_24h}
          icon={<Rocket size={14} />}
          hint={`${formatCoins(data.projects)} проектов всего`}
        />
        <StatTile
          label="Домены подтверждены"
          value={data.domains_verified}
          icon={<Globe size={14} />}
          hint={`${formatCoins(data.domains_pending)} ждут DNS`}
        />
        <StatTile
          label="Баланс на счетах"
          value={data.total_balance}
          icon={<Wallet size={14} />}
          hint={`${formatCoins(data.total_reserved)} зарезервировано`}
        />
        <StatTile
          label="Пополнено всего"
          value={data.coins_topped_up}
          icon={<Wallet size={14} />}
          hint="включая стартовые начисления"
        />
        <StatTile
          label="Списано всего"
          value={data.coins_spent}
          icon={<Wallet size={14} />}
          hint="по завершённым деплоям"
        />
        <StatTile
          label="Активные API-ключи"
          value={data.active_api_keys}
          icon={<KeyRound size={14} />}
        />
      </section>

      {/* The one tile that reports a condition rather than a quantity. It is
          amber *and* says why *and* carries an icon — the state never reads by
          color alone. */}
      {data.walletless_users > 0 && (
        <section className="grid grid-cols-1 lg:grid-cols-3 gap-3">
          <StatTile
            label="Аккаунты без кошелька"
            value={data.walletless_users}
            alert
            icon={<AlertTriangle size={14} />}
            hint="не могут деплоить"
          />
          <div className="lg:col-span-2 text-sm text-zinc-600 bg-amber-50 border border-amber-200 rounded-xl px-4 py-3.5 flex flex-col gap-1">
            <span className="font-medium text-amber-800">Seed-вебхук Supabase не срабатывает</span>
            <span>
              Пользователь зарегистрировался, но кошелёк не создан — деплой вернёт{' '}
              <code className="font-mono text-xs bg-white/60 rounded px-1">wallet_not_found</code>.
              Проверьте Database Webhook на{' '}
              <code className="font-mono text-xs">public.profiles</code> и значение{' '}
              <code className="font-mono text-xs">SUPABASE_WEBHOOK_SECRET</code>.
            </span>
          </div>
        </section>
      )}

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
