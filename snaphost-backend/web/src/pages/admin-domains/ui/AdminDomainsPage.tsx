import styles from './AdminDomainsPage.module.css';

import { useState } from 'react';
import { Link } from 'react-router-dom';
import { Search } from 'lucide-react';

import { domainTone, formatDateTime, shortId, useAdminDomains } from '@/entities/admin';
import { useDebounced } from '@/shared/lib/use-debounced';
import { AdminTable } from '@/shared/ui/admin-table';
import { Input } from '@/shared/ui/input';
import { Pager } from '@/shared/ui/pager';
import { StatusPill } from '@/shared/ui/status-pill';

const PAGE_SIZE = 50;

const STATUS_FILTERS = [
  { value: '', label: 'Все' },
  { value: 'verified', label: 'Подтверждённые' },
  { value: 'pending', label: 'Ждут DNS' },
  { value: 'failed', label: 'С ошибкой' },
  { value: 'revoked', label: 'Отключённые' },
];

/** Failure codes the verifier and the idle sweep write into
 *  custom_domains.last_error. The detail goes to the service log; this is the
 *  stable code, so it is translated here rather than shown raw. */
const LAST_ERROR_LABEL: Record<string, string> = {
  txt_not_found: 'TXT-запись не найдена',
  txt_mismatch: 'TXT-запись не совпадает',
  dns_lookup_failed: 'DNS не отвечает',
  unpinned_idle: 'снят с деплоя: нет трафика',
};

function AdminDomainsPage() {
  const [search, setSearch] = useState('');
  const [status, setStatus] = useState('');
  const [offset, setOffset] = useState(0);
  const debouncedSearch = useDebounced(search);

  const { data, isLoading, isError, isFetching } = useAdminDomains({
    q: debouncedSearch || undefined,
    status: status || undefined,
    limit: PAGE_SIZE,
    offset,
  });

  const domains = data?.items ?? [];

  return (
    <div className={`${styles.root} flex flex-col gap-4`}>
      <div className="flex flex-wrap items-end gap-3">
        <div className="w-full sm:w-80">
          <Input
            placeholder="Поиск по домену или email"
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
        headers={['Домен', 'Пользователь', 'Статус', 'Цель', 'Проверен', 'Проверка', 'Добавлен']}
        isLoading={isLoading}
        isError={isError}
        isEmpty={domains.length === 0}
        emptyText="Доменов нет"
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
        {domains.map((domain) => (
          <tr key={domain.id} className="border-b border-zinc-100 last:border-b-0 hover:bg-zinc-50">
            <td className="px-4 py-3 text-zinc-900">{domain.domain}</td>
            <td className="px-4 py-3">
              <Link
                to={`/dashboard/admin/users/${domain.user_id}`}
                className="text-zinc-600 hover:underline"
                title={domain.user_id}
              >
                {domain.user_email ?? shortId(domain.user_id)}
              </Link>
            </td>
            <td className="px-4 py-3">
              <StatusPill tone={domainTone(domain.status)}>{domain.status}</StatusPill>
              {domain.last_error && (
                <div className="text-xs text-zinc-400 mt-0.5">
                  {LAST_ERROR_LABEL[domain.last_error] ?? domain.last_error}
                </div>
              )}
            </td>
            <td className="px-4 py-3">
              {domain.target_deploy_id ? (
                <Link
                  to={`/dashboard/admin/deploys/${domain.target_deploy_id}`}
                  className="font-mono text-xs text-zinc-600 hover:underline"
                  title={domain.target_deploy_id}
                >
                  {shortId(domain.target_deploy_id)}
                </Link>
              ) : (
                <span
                  className="text-zinc-400"
                  title="Домен ни на что не указывает — трафик получит 404"
                >
                  не привязан
                </span>
              )}
            </td>
            <td className="px-4 py-3 text-zinc-500 whitespace-nowrap">
              {formatDateTime(domain.verified_at)}
            </td>
            <td className="px-4 py-3 text-zinc-500 whitespace-nowrap">
              {formatDateTime(domain.last_checked_at)}
            </td>
            <td className="px-4 py-3 text-zinc-500 whitespace-nowrap">
              {formatDateTime(domain.created_at)}
            </td>
          </tr>
        ))}
      </AdminTable>
    </div>
  );
}

export default AdminDomainsPage;
