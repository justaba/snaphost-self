import styles from './AdminUsersPage.module.css';

import { useState } from 'react';
import { Link } from 'react-router-dom';
import { AlertTriangle, Search } from 'lucide-react';

import { formatCoins, formatDate, formatDateTime, shortId, useAdminUsers } from '@/entities/admin';
import { useDebounced } from '@/shared/lib/use-debounced';
import { AdminTable } from '@/shared/ui/admin-table';
import { Input } from '@/shared/ui/input';
import { Pager } from '@/shared/ui/pager';

const PAGE_SIZE = 50;

function AdminUsersPage() {
  const [search, setSearch] = useState('');
  const [offset, setOffset] = useState(0);
  const debouncedSearch = useDebounced(search);

  const { data, isLoading, isError, isFetching } = useAdminUsers({
    q: debouncedSearch || undefined,
    limit: PAGE_SIZE,
    offset,
  });

  const users = data?.items ?? [];

  return (
    <div className={`${styles.root} flex flex-col gap-4`}>
      <div className="max-w-md">
        <Input
          placeholder="Поиск по email или UUID"
          value={search}
          onChange={(e) => {
            setSearch(e.target.value);
            setOffset(0);
          }}
          iconLeft={<Search size={16} />}
        />
      </div>

      <AdminTable
        headers={[
          'Пользователь',
          'Баланс',
          'Резерв',
          'Деплои',
          'Пополнено',
          'Списано',
          'Домены',
          'Последний деплой',
          'Регистрация',
        ]}
        isLoading={isLoading}
        isError={isError}
        isEmpty={users.length === 0}
        emptyText={debouncedSearch ? 'Никто не найден' : 'Пока нет ни одного аккаунта'}
        footer={
          data && (
            <Pager
              total={data.total}
              limit={data.limit}
              offset={data.offset}
              onOffsetChange={setOffset}
              isFetching={isFetching}
            />
          )
        }
      >
        {users.map((user) => (
          <tr key={user.id} className="border-b border-zinc-100 last:border-b-0 hover:bg-zinc-50">
            <td className="px-4 py-3">
              <Link
                to={`/dashboard/admin/users/${user.id}`}
                className="text-zinc-900 hover:underline"
              >
                {user.email ?? <span className="text-zinc-400">без email</span>}
              </Link>
              <div className="text-xs text-zinc-400 font-mono" title={user.id}>
                {shortId(user.id)}
              </div>
            </td>
            <td className="px-4 py-3 tabular-nums">
              {user.has_wallet ? (
                formatCoins(user.balance)
              ) : (
                <span
                  className="inline-flex items-center gap-1 text-amber-700"
                  title="Кошелёк не создан — seed-вебхук Supabase не сработал. Деплой вернёт wallet_not_found."
                >
                  <AlertTriangle size={13} />
                  нет кошелька
                </span>
              )}
            </td>
            <td className="px-4 py-3 tabular-nums text-zinc-500">
              {user.has_wallet ? formatCoins(user.reserved) : '—'}
            </td>
            <td className="px-4 py-3 tabular-nums text-zinc-500">
              {formatCoins(user.deploys_total)}
              {user.deploys_running > 0 && (
                <span className="text-emerald-600"> · {user.deploys_running} live</span>
              )}
              {user.deploys_failed > 0 && (
                <span className="text-red-500"> · {user.deploys_failed} fail</span>
              )}
            </td>
            <td className="px-4 py-3 tabular-nums text-zinc-500">
              {formatCoins(user.coins_topped_up)}
            </td>
            <td className="px-4 py-3 tabular-nums text-zinc-500">
              {formatCoins(user.coins_spent)}
            </td>
            <td className="px-4 py-3 tabular-nums text-zinc-500">{user.domains_count || '—'}</td>
            <td className="px-4 py-3 text-zinc-500 whitespace-nowrap">
              {formatDateTime(user.last_deploy_at)}
            </td>
            <td className="px-4 py-3 text-zinc-500 whitespace-nowrap">
              {formatDate(user.created_at)}
            </td>
          </tr>
        ))}
      </AdminTable>
    </div>
  );
}

export default AdminUsersPage;
