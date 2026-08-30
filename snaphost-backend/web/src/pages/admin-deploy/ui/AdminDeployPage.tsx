import styles from './AdminDeployPage.module.css';

import type { ReactNode } from 'react';
import { Link, useParams } from 'react-router-dom';
import { ArrowLeft, Check, ExternalLink, X } from 'lucide-react';

import {
  deployTone,
  domainTone,
  formatDateTime,
  shortId,
  SOURCE_TYPE_LABEL,
  useAdminDeploy,
} from '@/entities/admin';
import { AdminTable } from '@/shared/ui/admin-table';
import { Skeleton } from '@/shared/ui/skeleton';
import { StatusPill } from '@/shared/ui/status-pill';

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className={`${styles.root} flex flex-col gap-0.5 min-w-0`}>
      <span className="text-xs text-zinc-500">{label}</span>
      <span className="text-sm text-zinc-900 break-all">{children}</span>
    </div>
  );
}

/** A saga flag is a fact, not a health signal — it gets an icon and a word so
 *  it never reads by color alone. */
function Flag({ label, value }: { label: string; value: boolean }) {
  return (
    <div className="flex items-center gap-1.5 text-sm">
      {value ? (
        <Check size={15} className="text-emerald-600" />
      ) : (
        <X size={15} className="text-zinc-400" />
      )}
      <span className={value ? 'text-zinc-900' : 'text-zinc-500'}>{label}</span>
    </div>
  );
}

function AdminDeployPage() {
  const { deployId } = useParams<{ deployId: string }>();
  const { data: deploy, isLoading, isError } = useAdminDeploy(deployId);

  if (isError) {
    return (
      <p className="text-sm text-red-600 bg-red-50 border border-red-200 rounded-lg px-4 py-3">
        Деплой не найден или не удалось его загрузить.
      </p>
    );
  }

  if (isLoading || !deploy) {
    return (
      <div className="flex flex-col gap-4">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-40 w-full" />
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-5">
      <div>
        <Link
          to="/dashboard/admin/deploys"
          className="inline-flex items-center gap-1 text-sm text-zinc-500 hover:text-zinc-800"
        >
          <ArrowLeft size={15} />К списку
        </Link>
        <div className="flex items-center gap-3 mt-2">
          <h2 className="text-xl font-semibold text-zinc-900 font-mono">{shortId(deploy.id)}</h2>
          <StatusPill tone={deployTone(deploy.status)}>{deploy.status}</StatusPill>
        </div>
        <p className="font-mono text-xs text-zinc-400 mt-0.5 select-all">{deploy.id}</p>
      </div>

      {deploy.failure_reason && (
        <p className="text-sm text-red-700 bg-red-50 border border-red-200 rounded-lg px-4 py-3">
          {deploy.failure_reason}
        </p>
      )}

      <section className="bg-white border border-zinc-200 rounded-xl px-4 py-4 grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
        <Field label="Пользователь">
          <Link
            to={`/dashboard/admin/users/${deploy.user_id}`}
            className="text-zinc-900 hover:underline"
          >
            {deploy.user_email ?? shortId(deploy.user_id)}
          </Link>
        </Field>
        <Field label="Проект">
          {deploy.project_id ? (
            <>
              {deploy.project_slug ?? shortId(deploy.project_id)}
              <span className="text-zinc-400 text-xs"> · {shortId(deploy.project_id)}</span>
            </>
          ) : (
            <span className="text-zinc-400">без проекта (до Task 16a)</span>
          )}
        </Field>
        <Field label="Источник">
          {SOURCE_TYPE_LABEL[deploy.source_type] ?? deploy.source_type}
          {deploy.repo_url && <div className="text-xs text-zinc-500">{deploy.repo_url}</div>}
          {deploy.branch && <div className="text-xs text-zinc-500">ветка: {deploy.branch}</div>}
        </Field>
        <Field label="Публичный адрес">
          {deploy.endpoint_url ? (
            <a
              href={deploy.endpoint_url}
              target="_blank"
              rel="noreferrer noopener"
              className="inline-flex items-center gap-1 hover:underline"
            >
              {deploy.endpoint_url}
              <ExternalLink size={12} />
            </a>
          ) : (
            <span className="text-zinc-400">—</span>
          )}
        </Field>
        <Field label="Образ">
          {deploy.image_ref ?? <span className="text-zinc-400">не собран</span>}
        </Field>
        <Field label="Runtime">
          {deploy.container_id ?? <span className="text-zinc-400">нет контейнера</span>}
        </Field>
        <Field label="Создан">{formatDateTime(deploy.created_at)}</Field>
        <Field label="TTL истекает">{formatDateTime(deploy.ttl_expires_at)}</Field>
        <Field label="Последний запрос">
          {deploy.last_request_at ? (
            formatDateTime(deploy.last_request_at)
          ) : (
            <span
              className="text-zinc-400"
              title="Пишется при резолве маршрута, не чаще раза в 5 минут"
            >
              трафика не было
            </span>
          )}
        </Field>
        <Field label="Остановлен">{formatDateTime(deploy.stopped_at)}</Field>
        <Field label="Коммит">
          {deploy.commit_sha ? (
            <span className="font-mono text-xs">{deploy.commit_sha.slice(0, 12)}</span>
          ) : (
            <span className="text-zinc-400">—</span>
          )}
        </Field>
      </section>

      <section>
        <h3 className="text-sm font-medium text-zinc-900 mb-2">Сага</h3>
        {deploy.saga ? (
          <div className="bg-white border border-zinc-200 rounded-xl px-4 py-4 flex flex-col gap-3">
            <div className="flex flex-wrap items-center gap-3">
              <StatusPill tone="zinc">шаг: {deploy.saga.current_step}</StatusPill>
              {deploy.saga.retry_count > 0 && (
                <span className="text-sm text-zinc-500">повторов: {deploy.saga.retry_count}</span>
              )}
            </div>
            <div className="flex flex-wrap gap-x-6 gap-y-2">
              <Flag label="образ собран" value={deploy.saga.image_built} />
              <Flag label="контейнер запущен" value={deploy.saga.container_running} />
            </div>
            {deploy.saga.last_error && (
              <p className="text-sm text-zinc-600 bg-zinc-50 border border-zinc-200 rounded-lg px-3 py-2 break-all">
                {deploy.saga.last_error}
              </p>
            )}
            <div className="text-xs text-zinc-500">
              начата: {formatDateTime(deploy.saga.started_at)} · завершена:{' '}
              {formatDateTime(deploy.saga.completed_at)}
            </div>
          </div>
        ) : (
          <p className="text-sm text-zinc-500 bg-white border border-zinc-200 rounded-xl px-4 py-3">
            Саги нет — деплой не дошёл до воркера либо создан до саговой схемы.
          </p>
        )}
      </section>

      <section>
        <h3 className="text-sm font-medium text-zinc-900 mb-2">Домены на этом деплое</h3>
        <AdminTable
          headers={['Домен', 'Статус', 'Владелец', 'Проверен']}
          isEmpty={deploy.domains.length === 0}
          emptyText="Ни один домен не указывает на этот деплой"
        >
          {deploy.domains.map((domain) => (
            <tr key={domain.id} className="border-b border-zinc-100 last:border-b-0">
              <td className="px-4 py-3 text-zinc-900">{domain.domain}</td>
              <td className="px-4 py-3">
                <StatusPill tone={domainTone(domain.status)}>{domain.status}</StatusPill>
              </td>
              <td className="px-4 py-3">
                <Link
                  to={`/dashboard/admin/users/${domain.user_id}`}
                  className="text-zinc-600 hover:underline"
                >
                  {domain.user_email ?? shortId(domain.user_id)}
                </Link>
              </td>
              <td className="px-4 py-3 text-zinc-500 whitespace-nowrap">
                {formatDateTime(domain.verified_at)}
              </td>
            </tr>
          ))}
        </AdminTable>
      </section>
    </div>
  );
}

export default AdminDeployPage;
