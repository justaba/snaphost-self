import styles from './SettingsPage.module.css';

import { ScrollText } from 'lucide-react';

import { useAuditLog, type AuditEntry } from '@/entities/project';
import { useAuth } from '@/entities/session';
import { EmptyState } from '@/shared/ui/empty-state';
import { Skeleton } from '@/shared/ui/skeleton';

/** The audit log is the only record of a destructive action, and deleting a
 *  project cannot be undone from the panel. It was written and never read
 *  until this screen existed — the only way to see it was sqlite3.
 *
 *  It sits in Настройки rather than next to the delete button on purpose:
 *  reading what already happened is a different task from doing it, and
 *  putting a history under the button that writes it invites treating it as an
 *  undo, which it is not. */
function describe(entry: AuditEntry): string {
  const details = entry.details ?? {};
  const slug = typeof details.slug === 'string' ? details.slug : entry.target_id.slice(0, 8);
  const deploys = typeof details.deploys === 'number' ? details.deploys : 0;
  const domains = typeof details.domains === 'number' ? details.domains : 0;

  if (entry.action === 'project.delete') {
    return `Удалён проект ${slug}: сборок ${deploys}, доменов ${domains}`;
  }
  return `${entry.action} → ${entry.target_type} ${slug}`;
}

function formatMoment(iso: string): string {
  const date = new Date(iso);
  return Number.isNaN(date.getTime()) ? iso : date.toLocaleString('ru-RU');
}

function SettingsPage() {
  const { user } = useAuth();
  // The endpoint is admin-only; asking for it as a plain user is a guaranteed
  // 403, so the query is not started at all.
  const isAdmin = user?.role === 'admin';
  const auditQuery = useAuditLog(isAdmin);

  return (
    <div className={`${styles.root} max-w-7xl mx-auto`}>
      <header className="mb-6">
        <h1 className="text-2xl font-semibold text-zinc-900 tracking-tight">Настройки</h1>
        <p className="text-sm text-zinc-500 mt-1">Аккаунт оператора и журнал действий</p>
      </header>

      <section className="mb-8 rounded-xl border border-zinc-200 bg-white p-5">
        <h2 className="text-sm font-medium text-zinc-900">Аккаунт</h2>
        <dl className="mt-3 grid grid-cols-[auto_1fr] gap-x-6 gap-y-1 text-sm">
          <dt className="text-zinc-500">Email</dt>
          <dd className="text-zinc-900">{user?.email ?? '—'}</dd>
          <dt className="text-zinc-500">Роль</dt>
          <dd className="text-zinc-900">{user?.role ?? '—'}</dd>
        </dl>
        <p className="mt-3 text-xs text-zinc-500">
          Пароль печатается один раз при первом запуске и не восстанавливается — его можно только
          сменить.
        </p>
      </section>

      <section className="rounded-xl border border-zinc-200 bg-white p-5">
        <h2 className="text-sm font-medium text-zinc-900">Журнал действий</h2>
        <p className="mt-1 text-xs text-zinc-500">
          Необратимые операции. Запись создаётся той же транзакцией, что и само изменение, поэтому
          действие не может остаться без следа.
        </p>

        {!isAdmin && <p className="mt-4 text-sm text-zinc-500">Журнал доступен только админу.</p>}

        {isAdmin && auditQuery.isLoading && (
          <div className="mt-4 flex flex-col gap-2">
            {Array.from({ length: 3 }, (_, i) => (
              <Skeleton key={i} className="h-10 w-full" />
            ))}
          </div>
        )}

        {isAdmin && auditQuery.isError && (
          <p className="mt-4 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-800">
            Не удалось загрузить журнал.
          </p>
        )}

        {isAdmin && !auditQuery.isLoading && !auditQuery.isError && (
          <>
            {(auditQuery.data?.items.length ?? 0) === 0 ? (
              <div className="mt-4">
                <EmptyState
                  icon={<ScrollText className="w-12 h-12" strokeWidth={1.5} />}
                  title="Пока ничего не удаляли"
                  description="Здесь появятся удаления проектов с указанием, кто и что убрал."
                />
              </div>
            ) : (
              <ul className="mt-4 flex flex-col divide-y divide-zinc-100">
                {auditQuery.data?.items.map((entry) => (
                  <li key={entry.id} className="flex items-start justify-between gap-4 py-3">
                    <span className="text-sm text-zinc-900">{describe(entry)}</span>
                    <span className="shrink-0 whitespace-nowrap text-xs text-zinc-500">
                      {formatMoment(entry.created_at)}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </>
        )}
      </section>
    </div>
  );
}

export default SettingsPage;
