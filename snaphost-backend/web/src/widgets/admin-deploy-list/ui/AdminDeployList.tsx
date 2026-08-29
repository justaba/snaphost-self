import styles from './AdminDeployList.module.css';

import { useState } from 'react';
import { Link } from 'react-router-dom';
import { ExternalLink, Search } from 'lucide-react';

import {
  deployTone,
  formatCoins,
  formatDateTime,
  shortId,
  SOURCE_TYPE_LABEL,
  useAdminDeploys,
} from '@/entities/admin';
import { useDebounced } from '@/shared/lib/use-debounced';
import { AdminTable } from '@/shared/ui/admin-table';
import { Input } from '@/shared/ui/input';
import { Pager } from '@/shared/ui/pager';
import { StatusPill } from '@/shared/ui/status-pill';

const PAGE_SIZE = 50;

const STATUS_FILTERS = [
  { value: '', label: 'Все' },
  { value: 'running', label: 'Запущенные' },
  { value: 'failed', label: 'С ошибкой' },
  { value: 'building', label: 'Сборка' },
  { value: 'pending', label: 'В очереди' },
  { value: 'stopped', label: 'Остановленные' },
  { value: 'deleted', label: 'Удалённые' },
];

export interface AdminDeployListProps {
  /** Renders the same table scoped to one account, without its own header. */
  userId?: string;
}

function AdminDeployList({ userId }: AdminDeployListProps) {
  const [search, setSearch] = useState('');
  const [status, setStatus] = useState('');
  const [offset, setOffset] = useState(0);
  const debouncedSearch = useDebounced(search);

  const { data, isLoading, isError, isFetching } = useAdminDeploys({
    q: debouncedSearch || undefined,
    status: status || undefined,
    userId,
    limit: PAGE_SIZE,
    offset,
  });

  const deploys = data?.items ?? [];

  return (
    <div className={`${styles.root} flex flex-col gap-4`}>
      <div className="flex flex-wrap items-end gap-3">
        <div className="w-full sm:w-80">
          <Input
            placeholder="Поиск по UUID, репозиторию, поддомену"
            value={search}
            onChange={(e) => {
              setSearch(e.target.value);
              setOffset(0);
            }}
            iconLeft={<Search size={16} />}
          />
        </div>
        <div className="flex flex-wrap gap-1">
          {STATUS_FILTERS.map((filter) => (
            <button
              key={filter.value || 'all'}
              type="button"
              onClick={() => {
                setStatus(filter.value);
                setOffset(0);
              }}
              className={[
                'px-3 py-1.5 rounded-lg text-sm border transition-colors',
                status === filter.value
                  ? 'bg-zinc-900 text-white border-zinc-900'
                  : 'bg-white text-zinc-600 border-zinc-200 hover:bg-zinc-50',
              ].join(' ')}
            >
              {filter.label}
            </button>
          ))}
        </div>
      </div>

      <AdminTable
        headers={['Деплой', 'Пользователь', 'Источник', 'Статус', 'Стоимость', 'Адрес', 'Создан']}
        isLoading={isLoading}
        isError={isError}
        isEmpty={deploys.length === 0}
        emptyText="Деплои не найдены"
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
        {deploys.map((deploy) => (
          <tr key={deploy.id} className="border-b border-zinc-100 last:border-b-0 hover:bg-zinc-50">
            <td className="px-4 py-3">
              <Link
                to={`/dashboard/admin/deploys/${deploy.id}`}
                className="font-mono text-xs text-zinc-900 hover:underline"
                title={deploy.id}
              >
                {shortId(deploy.id)}
              </Link>
              {deploy.project_slug && (
                <div className="text-xs text-zinc-400 truncate max-w-[16rem]">
                  {deploy.project_slug}
                </div>
              )}
            </td>
            <td className="px-4 py-3">
              <Link
                to={`/dashboard/admin/users/${deploy.user_id}`}
                className="text-zinc-600 hover:underline"
                title={deploy.user_id}
              >
                {deploy.user_email ?? shortId(deploy.user_id)}
              </Link>
            </td>
            <td className="px-4 py-3 text-zinc-500">
              <div>{SOURCE_TYPE_LABEL[deploy.source_type] ?? deploy.source_type}</div>
              {deploy.repo_url && (
                <div
                  className="text-xs text-zinc-400 truncate max-w-[18rem]"
                  title={deploy.repo_url}
                >
                  {deploy.repo_url}
                </div>
              )}
            </td>
            <td className="px-4 py-3">
              <StatusPill tone={deployTone(deploy.status)} title={deploy.failure_reason}>
                {deploy.status}
              </StatusPill>
            </td>
            <td className="px-4 py-3 tabular-nums text-zinc-500">
              {formatCoins(deploy.cost_vibecoins)}
            </td>
            <td className="px-4 py-3">
              {deploy.endpoint_url ? (
                <a
                  href={deploy.endpoint_url}
                  target="_blank"
                  rel="noreferrer noopener"
                  className="inline-flex items-center gap-1 text-zinc-600 hover:underline text-xs"
                >
                  {deploy.subdomain ?? 'открыть'}
                  <ExternalLink size={12} />
                </a>
              ) : (
                <span className="text-zinc-400">—</span>
              )}
            </td>
            <td className="px-4 py-3 text-zinc-500 whitespace-nowrap">
              {formatDateTime(deploy.created_at)}
            </td>
          </tr>
        ))}
      </AdminTable>
    </div>
  );
}

export default AdminDeployList;
