import styles from './ProjectDetailsPage.module.css';

import { useMemo, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { ArrowLeft, ArrowUpRight, Globe, Plus, RefreshCw } from 'lucide-react';

import { useDeploys } from '@/entities/deploy';
import { describeDomain, toneClasses } from '@/entities/domain';
import { findProject, groupDeploysByProject } from '@/entities/project';
import { AttachDomainModal, useDomains } from '@/features/manage-domain';
import { Button } from '@/shared/ui/button';
import { EmptyState } from '@/shared/ui/empty-state';
import { Skeleton } from '@/shared/ui/skeleton';
import { useToast } from '@/shared/ui/toast';
import { ProjectDeployList, ProjectDetailModal } from '@/widgets/project-overview';

/**
 * One project: its attached domains and its build history. The project is the
 * permanent thing — deploys under it come and go, and a domain points at
 * whichever one it was last aimed at.
 */
function ProjectPage() {
  const { projectId = '' } = useParams<{ projectId: string }>();
  const deploysQuery = useDeploys({ limit: 100 });
  const domainsQuery = useDomains();
  const { toast } = useToast();

  const [attachOpen, setAttachOpen] = useState(false);
  const [detailDeployId, setDetailDeployId] = useState<string | null>(null);

  const projects = useMemo(
    () => groupDeploysByProject(deploysQuery.data?.deploys ?? []),
    [deploysQuery.data],
  );
  const project = findProject(projects, projectId);

  const domains = useMemo(
    () => (domainsQuery.data?.domains ?? []).filter((d) => d.project_id === projectId),
    [domainsQuery.data, projectId],
  );
  const verifiedDomains = domains.filter((d) => d.status === 'verified');

  const isLoading = deploysQuery.isLoading || domainsQuery.isLoading;

  if (isLoading) {
    return (
      <div className={`${styles.root} max-w-7xl mx-auto flex flex-col gap-4`}>
        <Skeleton className="h-10 w-64" />
        <Skeleton className="h-24 w-full" />
        <Skeleton className="h-40 w-full" />
      </div>
    );
  }

  if (deploysQuery.isError) {
    return (
      <div className="max-w-7xl mx-auto rounded-lg border border-red-200 bg-red-50 p-4 flex items-center justify-between gap-4">
        <div className="text-sm text-red-800">Не удалось загрузить проект.</div>
        <Button
          variant="secondary"
          size="sm"
          iconLeft={<RefreshCw size={14} />}
          onClick={() => deploysQuery.refetch()}
        >
          Повторить
        </Button>
      </div>
    );
  }

  if (!project) {
    return (
      <div className="max-w-7xl mx-auto flex flex-col gap-4">
        <Link
          to="/dashboard/projects"
          className="inline-flex items-center gap-1.5 text-sm text-zinc-500 hover:text-zinc-900 w-fit"
        >
          <ArrowLeft size={15} /> К проектам
        </Link>
        <EmptyState
          title="Проект не найден"
          description="Возможно, все его деплои удалены или он принадлежит другому аккаунту."
        />
      </div>
    );
  }

  const latest = project.deploys[0];
  const running = project.deploys.filter((d) => d.status === 'running');

  return (
    <div className="max-w-7xl mx-auto flex flex-col gap-6">
      <div>
        <Link
          to="/dashboard/projects"
          className="inline-flex items-center gap-1.5 text-sm text-zinc-500 hover:text-zinc-900 w-fit"
        >
          <ArrowLeft size={15} /> К проектам
        </Link>
      </div>

      <header className="flex items-start justify-between gap-4 flex-wrap">
        <div className="min-w-0">
          <h1 className="text-2xl font-semibold text-zinc-900 tracking-tight break-all">
            {project.label}
          </h1>
          <p className="text-sm text-zinc-500 mt-1">
            {project.deploys.length} деплоев · {running.length} запущено
            {latest?.repo_url && (
              <>
                {' · '}
                <a
                  href={latest.repo_url}
                  target="_blank"
                  rel="noreferrer noopener"
                  className="hover:text-zinc-900"
                >
                  репозиторий
                </a>
              </>
            )}
          </p>
        </div>
        <Button iconLeft={<Plus size={16} />} onClick={() => setAttachOpen(true)}>
          Привязать домен
        </Button>
      </header>

      <section className="flex flex-col gap-3">
        <h2 className="text-sm font-medium text-zinc-900">Домены</h2>

        {domains.length === 0 && (
          <EmptyState
            icon={<Globe className="w-12 h-12" strokeWidth={1.5} />}
            title="Домен не привязан"
            description="Пока проект открывается только по сгенерированным адресам деплоев. Привяжите свой домен — он переживёт пересборку, потому что указывает на проект."
            action={
              <Button iconLeft={<Plus size={16} />} onClick={() => setAttachOpen(true)}>
                Привязать домен
              </Button>
            }
          />
        )}

        {domains.map((domain) => {
          const state = describeDomain(domain);
          return (
            <div
              key={domain.id}
              className="bg-white border border-zinc-200 rounded-xl px-5 py-4 flex items-start justify-between gap-4 flex-wrap"
            >
              <div className="min-w-0 flex flex-col gap-1">
                <div className="flex items-center gap-2 flex-wrap">
                  <span className="text-base font-medium text-zinc-900 break-all">
                    {domain.domain}
                  </span>
                  <span
                    className={[
                      'text-xs font-medium px-2 py-0.5 rounded-full border',
                      toneClasses(state.tone),
                    ].join(' ')}
                  >
                    {state.label}
                  </span>
                </div>
                <p className="text-sm text-zinc-600 max-w-2xl">{state.detail}</p>
              </div>
              <Link
                to="/dashboard/domains"
                className="inline-flex items-center gap-1 text-sm text-zinc-500 hover:text-zinc-900"
              >
                DNS и настройки
                <ArrowUpRight size={14} />
              </Link>
            </div>
          );
        })}
      </section>

      <section className="flex flex-col gap-3">
        <div className="flex items-baseline justify-between gap-4">
          <h2 className="text-sm font-medium text-zinc-900">Деплои</h2>
          <p className="text-xs text-zinc-500">
            Каждый деплой сохраняет свой адрес — переключение домена не пересобирает проект
          </p>
        </div>
        <ProjectDeployList
          deploys={project.deploys}
          domains={verifiedDomains}
          onOpenDeploy={setDetailDeployId}
        />
      </section>

      <AttachDomainModal
        isOpen={attachOpen}
        onClose={() => setAttachOpen(false)}
        projects={projects}
        fixedProjectId={project.projectId}
        onAttached={(domain) =>
          toast(`Домен ${domain} добавлен. Осталось добавить TXT-запись.`, 'success')
        }
      />

      <ProjectDetailModal
        isOpen={detailDeployId !== null}
        deployId={detailDeployId}
        onClose={() => setDetailDeployId(null)}
      />
    </div>
  );
}

export default ProjectPage;
