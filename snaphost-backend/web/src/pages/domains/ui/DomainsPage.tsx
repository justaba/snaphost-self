import styles from './DomainsPage.module.css';

import { useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { ArrowUpRight, Globe, Plus, RefreshCw, Trash2 } from 'lucide-react';

import { useDeploys } from '@/entities/deploy';
import { describeDomain, DnsRecords, toneClasses, type CustomDomain } from '@/entities/domain';
import { findProject, groupDeploysByProject } from '@/entities/project';
import {
  AttachDomainModal,
  RepointDomainModal,
  useDetachDomain,
  useDomains,
} from '@/features/manage-domain';
import { Button } from '@/shared/ui/button';
import { EmptyState } from '@/shared/ui/empty-state';
import { Modal } from '@/shared/ui/modal';
import { Skeleton } from '@/shared/ui/skeleton';
import { useToast } from '@/shared/ui/toast';

function DomainsPage() {
  const domainsQuery = useDomains();
  const deploysQuery = useDeploys({ limit: 100 });
  const detach = useDetachDomain();
  const { toast } = useToast();

  const [attachOpen, setAttachOpen] = useState(false);
  const [repointTarget, setRepointTarget] = useState<CustomDomain | null>(null);
  const [detachTarget, setDetachTarget] = useState<CustomDomain | null>(null);

  const projects = useMemo(
    () => groupDeploysByProject(deploysQuery.data?.deploys ?? []),
    [deploysQuery.data],
  );

  const domains = domainsQuery.data?.domains ?? [];
  const limit = domainsQuery.data?.limit ?? 0;
  const limitReached = limit > 0 && domains.length >= limit;

  const onDetach = async () => {
    if (!detachTarget) return;
    try {
      await detach.mutateAsync(detachTarget.id);
      setDetachTarget(null);
      toast('Домен отвязан.', 'success');
    } catch {
      toast('Не удалось отвязать домен.', 'error');
    }
  };

  return (
    <div className={`${styles.root} max-w-7xl mx-auto flex flex-col gap-6`}>
      <header className="flex items-start justify-between gap-4 flex-wrap">
        <div>
          <h1 className="text-2xl font-semibold text-zinc-900 tracking-tight">Домены</h1>
          <p className="text-sm text-zinc-500 mt-1">
            Свой домен для проекта. Домен привязывается к проекту, поэтому переживает пересборку
            {limit > 0 && ` · ${domains.length} из ${limit}`}
          </p>
        </div>
        <Button
          iconLeft={<Plus size={16} />}
          disabled={limitReached}
          title={limitReached ? 'Достигнут лимит доменов на аккаунте' : undefined}
          onClick={() => setAttachOpen(true)}
        >
          Привязать домен
        </Button>
      </header>

      {domainsQuery.isLoading && (
        <div className="flex flex-col gap-3">
          <Skeleton className="h-32 w-full" />
          <Skeleton className="h-32 w-full" />
        </div>
      )}

      {domainsQuery.isError && !domainsQuery.isLoading && (
        <div className="rounded-lg border border-red-200 bg-red-50 p-4 flex items-center justify-between gap-4">
          <div className="text-sm text-red-800">Не удалось загрузить список доменов.</div>
          <Button
            variant="secondary"
            size="sm"
            iconLeft={<RefreshCw size={14} />}
            onClick={() => domainsQuery.refetch()}
          >
            Повторить
          </Button>
        </div>
      )}

      {!domainsQuery.isLoading && !domainsQuery.isError && domains.length === 0 && (
        <EmptyState
          icon={<Globe className="w-16 h-16" strokeWidth={1.5} />}
          title="Нет привязанных доменов"
          description="Привяжите домен, который у вас уже есть, — мы покажем, какие DNS-записи добавить."
          action={
            <Button iconLeft={<Plus size={16} />} onClick={() => setAttachOpen(true)}>
              Привязать домен
            </Button>
          }
        />
      )}

      {domains.map((domain) => {
        const state = describeDomain(domain);
        const project = findProject(projects, domain.project_id);
        const target = project?.deploys.find((d) => d.id === domain.target_deploy_id);

        return (
          <section
            key={domain.id}
            className="bg-white border border-zinc-200 rounded-xl overflow-hidden"
          >
            <div className="flex items-start justify-between gap-4 flex-wrap px-5 py-4 border-b border-zinc-100">
              <div className="min-w-0 flex flex-col gap-1.5">
                <div className="flex items-center gap-2 flex-wrap">
                  <h2 className="text-base font-medium text-zinc-900 break-all">{domain.domain}</h2>
                  <span
                    className={[
                      'text-xs font-medium px-2 py-0.5 rounded-full border',
                      toneClasses(state.tone),
                    ].join(' ')}
                  >
                    {state.label}
                  </span>
                  {domain.status === 'verified' && domain.target_deploy_id && (
                    <a
                      href={`https://${domain.domain}`}
                      target="_blank"
                      rel="noreferrer"
                      className="inline-flex items-center gap-1 text-xs text-zinc-500 hover:text-zinc-900"
                    >
                      Открыть
                      <ArrowUpRight size={12} />
                    </a>
                  )}
                </div>
                <p className="text-sm text-zinc-600 max-w-2xl">{state.detail}</p>
                <p className="text-xs text-zinc-500">
                  Проект:{' '}
                  {project ? (
                    <Link
                      to={`/dashboard/projects/${project.projectId}`}
                      className="text-zinc-700 hover:text-zinc-900 underline underline-offset-2"
                    >
                      {project.label}
                    </Link>
                  ) : (
                    'неизвестен'
                  )}
                  {target && (
                    <>
                      {' · '}деплой{' '}
                      <span className="font-mono">
                        {target.commit_sha ? target.commit_sha.slice(0, 7) : target.id.slice(0, 8)}
                      </span>
                    </>
                  )}
                </p>
              </div>

              <div className="flex items-center gap-2">
                <Button variant="secondary" size="sm" onClick={() => setRepointTarget(domain)}>
                  {domain.target_deploy_id ? 'Сменить деплой' : 'Выбрать деплой'}
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  iconLeft={<Trash2 size={15} />}
                  onClick={() => setDetachTarget(domain)}
                  aria-label={`Отвязать домен ${domain.domain}`}
                >
                  Отвязать
                </Button>
              </div>
            </div>

            <div className="px-5 py-4">
              <h3 className="text-xs uppercase tracking-wide text-zinc-500 mb-2">DNS-записи</h3>
              <DnsRecords domain={domain} />
            </div>
          </section>
        );
      })}

      <AttachDomainModal
        isOpen={attachOpen}
        onClose={() => setAttachOpen(false)}
        projects={projects}
        onAttached={(domain) =>
          toast(`Домен ${domain} добавлен. Осталось добавить TXT-запись.`, 'success')
        }
      />

      <RepointDomainModal
        domain={repointTarget}
        project={repointTarget ? findProject(projects, repointTarget.project_id) : undefined}
        onClose={() => setRepointTarget(null)}
        onDone={() => toast('Домен переключён на выбранный деплой.', 'success')}
      />

      <Modal
        isOpen={detachTarget !== null}
        onClose={() => setDetachTarget(null)}
        title="Отвязать домен?"
        size="sm"
      >
        <div className="px-6 py-5 flex flex-col gap-4">
          <p className="text-sm text-zinc-600">
            «{detachTarget?.domain}» перестанет открываться сразу. Сам деплой продолжит работать по
            своему адресу. Привязать домен заново можно в любой момент — понадобится снова
            подтвердить владение.
          </p>
          <div className="flex justify-end gap-2">
            <Button variant="secondary" onClick={() => setDetachTarget(null)}>
              Отмена
            </Button>
            <Button variant="danger" loading={detach.isPending} onClick={onDetach}>
              Отвязать
            </Button>
          </div>
        </div>
      </Modal>
    </div>
  );
}

export default DomainsPage;
