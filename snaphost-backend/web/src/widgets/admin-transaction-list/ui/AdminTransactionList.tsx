import styles from './AdminTransactionList.module.css';

import { useState } from 'react';
import { Link } from 'react-router-dom';

import {
  formatCoins,
  formatDateTime,
  ledgerTone,
  LEDGER_TYPE_LABEL,
  shortId,
  useAdminTransactions,
} from '@/entities/admin';
import { AdminTable } from '@/shared/ui/admin-table';
import { Pager } from '@/shared/ui/pager';
import { StatusPill } from '@/shared/ui/status-pill';

const PAGE_SIZE = 50;

const TYPE_FILTERS = [
  { value: '', label: 'Все' },
  { value: 'topup', label: 'Пополнения' },
  { value: 'bonus', label: 'Бонусы' },
  { value: 'reserve', label: 'Резервы' },
  { value: 'commit', label: 'Списания' },
  { value: 'refund', label: 'Возвраты' },
];

export interface AdminTransactionListProps {
  /** Renders the same ledger scoped to one account. */
  userId?: string;
}

function AdminTransactionList({ userId }: AdminTransactionListProps) {
  const [type, setType] = useState('');
  const [offset, setOffset] = useState(0);

  const { data, isLoading, isError, isFetching } = useAdminTransactions({
    type: type || undefined,
    userId,
    limit: PAGE_SIZE,
    offset,
  });

  const entries = data?.items ?? [];

  return (
    <div className={`${styles.root} flex flex-col gap-4`}>
      <div className="flex flex-wrap gap-1">
        {TYPE_FILTERS.map((filter) => (
          <button
            key={filter.value || 'all'}
            type="button"
            onClick={() => {
              setType(filter.value);
              setOffset(0);
            }}
            className={[
              'px-3 py-1.5 rounded-lg text-sm border transition-colors',
              type === filter.value
                ? 'bg-zinc-900 text-white border-zinc-900'
                : 'bg-white text-zinc-600 border-zinc-200 hover:bg-zinc-50',
            ].join(' ')}
          >
            {filter.label}
          </button>
        ))}
      </div>

      <p className="text-xs text-zinc-500">
        Реестр показан как есть, без сворачивания: резерв без парного списания или возврата — это
        то, что здесь и нужно замечать.
      </p>

      <AdminTable
        headers={['Операция', 'Пользователь', 'Тип', 'Сумма', 'Статус', 'Деплой', 'Создана']}
        isLoading={isLoading}
        isError={isError}
        isEmpty={entries.length === 0}
        emptyText="Операций нет"
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
        {entries.map((entry) => (
          <tr key={entry.id} className="border-b border-zinc-100 last:border-b-0 hover:bg-zinc-50">
            <td className="px-4 py-3 font-mono text-xs text-zinc-500" title={entry.id}>
              {shortId(entry.id)}
            </td>
            <td className="px-4 py-3">
              <Link
                to={`/dashboard/admin/users/${entry.user_id}`}
                className="text-zinc-600 hover:underline"
                title={entry.user_id}
              >
                {entry.user_email ?? shortId(entry.user_id)}
              </Link>
            </td>
            <td className="px-4 py-3">
              <StatusPill tone={ledgerTone(entry.type)}>
                {LEDGER_TYPE_LABEL[entry.type] ?? entry.type}
              </StatusPill>
            </td>
            <td className="px-4 py-3 tabular-nums text-zinc-900">{formatCoins(entry.amount)}</td>
            <td className="px-4 py-3 text-zinc-500">{entry.status}</td>
            <td className="px-4 py-3">
              {entry.deploy_id ? (
                <Link
                  to={`/dashboard/admin/deploys/${entry.deploy_id}`}
                  className="font-mono text-xs text-zinc-600 hover:underline"
                  title={entry.deploy_id}
                >
                  {shortId(entry.deploy_id)}
                </Link>
              ) : (
                <span className="text-zinc-400">—</span>
              )}
            </td>
            <td className="px-4 py-3 text-zinc-500 whitespace-nowrap">
              {formatDateTime(entry.created_at)}
            </td>
          </tr>
        ))}
      </AdminTable>
    </div>
  );
}

export default AdminTransactionList;
