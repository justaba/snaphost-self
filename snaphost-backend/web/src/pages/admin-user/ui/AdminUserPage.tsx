import styles from './AdminUserPage.module.css';

import { useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { ArrowLeft } from 'lucide-react';

import { domainTone, formatCount, formatDateTime, shortId, useAdminUser } from '@/entities/admin';
import { AdminTable } from '@/shared/ui/admin-table';
import { Skeleton } from '@/shared/ui/skeleton';
import { StatTile } from '@/shared/ui/stat-tile';
import { StatusPill } from '@/shared/ui/status-pill';
import { Tabs } from '@/shared/ui/tabs';
import { AdminDeployList } from '@/widgets/admin-deploy-list';

type TabValue = 'deploys' | 'projects' | 'domains' | 'keys';

const TAB_ITEMS = [
  { value: 'deploys', label: 'Деплои' },
  { value: 'projects', label: 'Проекты' },
  { value: 'domains', label: 'Домены' },
  { value: 'keys', label: 'API-ключи' },
];

function AdminUserPage() {
  const { userId } = useParams<{ userId: string }>();
  const { data: user, isLoading, isError } = useAdminUser(userId);
  const [tab, setTab] = useState<TabValue>('deploys');

  if (isError) {
    return (
      <p
        className={`${styles.root} text-sm text-red-600 bg-red-50 border border-red-200 rounded-lg px-4 py-3`}
      >
        Аккаунт не найден или не удалось его загрузить.
      </p>
    );
  }

  if (isLoading || !user) {
    return (
      <div className="flex flex-col gap-4">
        <Skeleton className="h-8 w-64" />
        <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
          {Array.from({ length: 4 }, (_, i) => (
            <Skeleton key={i} className="h-24 w-full" />
          ))}
        </div>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-5">
      <div>
        <Link
          to="/dashboard/admin/users"
          className="inline-flex items-center gap-1 text-sm text-zinc-500 hover:text-zinc-800"
        >
          <ArrowLeft size={15} />К списку
        </Link>
        <h2 className="text-xl font-semibold text-zinc-900 mt-2">
          {user.email ?? <span className="text-zinc-400">аккаунт без email</span>}
        </h2>
        <p className="font-mono text-xs text-zinc-400 mt-0.5 select-all">{user.id}</p>
      </div>

      <section className="grid grid-cols-2 lg:grid-cols-3 gap-3">
        <StatTile
          label="Деплои"
          value={user.deploys_total}
          hint={`${formatCount(user.deploys_running)} запущено · ${formatCount(user.deploys_failed)} с ошибкой`}
        />
        <StatTile label="Домены" value={user.domains_count} hint="привязано к аккаунту" />
        <StatTile label="API-ключи" value={user.api_keys.length} hint="активных" />
      </section>

      <div className="text-sm text-zinc-500">
        Регистрация: {formatDateTime(user.created_at)} · последний деплой:{' '}
        {formatDateTime(user.last_deploy_at)}
      </div>

      <Tabs
        items={TAB_ITEMS}
        value={tab}
        onChange={(value) => setTab(value as TabValue)}
        ariaLabel="Разделы аккаунта"
      />

      {tab === 'deploys' && <AdminDeployList userId={user.id} />}

      {tab === 'projects' && (
        <AdminTable
          headers={['Проект', 'Источник', 'Сборок', 'Создан']}
          isEmpty={user.projects.length === 0}
          emptyText="У аккаунта нет проектов"
        >
          {user.projects.map((project) => (
            <tr key={project.id} className="border-b border-zinc-100 last:border-b-0">
              <td className="px-4 py-3 text-zinc-900">{project.slug}</td>
              <td className="px-4 py-3 text-zinc-500 font-mono text-xs break-all max-w-md">
                {project.source_key}
              </td>
              <td className="px-4 py-3 tabular-nums text-zinc-500">{project.deploys_count}</td>
              <td className="px-4 py-3 text-zinc-500 whitespace-nowrap">
                {formatDateTime(project.created_at)}
              </td>
            </tr>
          ))}
        </AdminTable>
      )}

      {tab === 'domains' && (
        <AdminTable
          headers={['Домен', 'Статус', 'Цель', 'Проверен']}
          isEmpty={user.domains.length === 0}
          emptyText="У аккаунта нет доменов"
        >
          {user.domains.map((domain) => (
            <tr key={domain.id} className="border-b border-zinc-100 last:border-b-0">
              <td className="px-4 py-3 text-zinc-900">{domain.domain}</td>
              <td className="px-4 py-3">
                <StatusPill tone={domainTone(domain.status)}>{domain.status}</StatusPill>
              </td>
              <td className="px-4 py-3">
                {domain.target_deploy_id ? (
                  <Link
                    to={`/dashboard/admin/deploys/${domain.target_deploy_id}`}
                    className="font-mono text-xs text-zinc-600 hover:underline"
                  >
                    {shortId(domain.target_deploy_id)}
                  </Link>
                ) : (
                  <span className="text-zinc-400">не привязан</span>
                )}
              </td>
              <td className="px-4 py-3 text-zinc-500 whitespace-nowrap">
                {formatDateTime(domain.verified_at)}
              </td>
            </tr>
          ))}
        </AdminTable>
      )}

      {tab === 'keys' && (
        <AdminTable
          headers={['Название', 'Префикс', 'Создан', 'Использован', 'Отозван']}
          isEmpty={user.api_keys.length === 0}
          emptyText="У аккаунта нет API-ключей"
        >
          {user.api_keys.map((key) => (
            <tr key={key.id} className="border-b border-zinc-100 last:border-b-0">
              <td className="px-4 py-3 text-zinc-900">{key.name || 'Без названия'}</td>
              <td className="px-4 py-3 font-mono text-xs text-zinc-500">{key.prefix}…</td>
              <td className="px-4 py-3 text-zinc-500 whitespace-nowrap">
                {formatDateTime(key.created_at)}
              </td>
              <td className="px-4 py-3 text-zinc-500 whitespace-nowrap">
                {formatDateTime(key.last_used_at)}
              </td>
              <td className="px-4 py-3 text-zinc-500 whitespace-nowrap">
                {key.revoked_at ? formatDateTime(key.revoked_at) : '—'}
              </td>
            </tr>
          ))}
        </AdminTable>
      )}
    </div>
  );
}

export default AdminUserPage;
