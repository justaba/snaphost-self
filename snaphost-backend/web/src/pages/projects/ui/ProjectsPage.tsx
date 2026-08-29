import styles from './ProjectsPage.module.css';

import { useMemo, useState } from 'react';
import { Plus, FolderOpen, RefreshCw } from 'lucide-react';
import { useDeploys, type DeployStatus, type DeploySummary } from '@/entities/deploy';
import type { CustomDomain } from '@/entities/domain';
import { groupDeploysByProject } from '@/entities/project';
import { useDomains } from '@/features/manage-domain';
import { Button } from '@/shared/ui/button';
import { EmptyState } from '@/shared/ui/empty-state';
import { Skeleton } from '@/shared/ui/skeleton';
import { Tabs } from '@/shared/ui/tabs';
import { DeployModal } from '@/widgets/deploy-launch';
import { ProjectDetailModal, ProjectGrid, ProjectSummaryCard } from '@/widgets/project-overview';

type FilterKey = 'all' | 'running' | 'in_progress' | 'inactive';

const EMPTY_DEPLOYS: DeploySummary[] = [];

const FILTERS: Array<{ value: FilterKey; label: string }> = [
  { value: 'all', label: 'Все' },
  { value: 'running', label: 'Запущенные' },
  { value: 'in_progress', label: 'В процессе' },
  { value: 'inactive', label: 'Остановлены' },
];

const IN_PROGRESS_SET: DeployStatus[] = [
  'pending',
  'reserved',
  'building',
  'built',
  'provisioning',
];
const INACTIVE_SET: DeployStatus[] = ['failed', 'stopped', 'deleted'];

function applyFilter(items: DeploySummary[], filter: FilterKey): DeploySummary[] {
  switch (filter) {
    case 'running':
      return items.filter((d) => d.status === 'running');
    case 'in_progress':
      return items.filter((d) => IN_PROGRESS_SET.includes(d.status));
    case 'inactive':
      return items.filter((d) => INACTIVE_SET.includes(d.status));
    default:
      return items;
  }
}

function ProjectsPage() {
  const [filter, setFilter] = useState<FilterKey>('all');
  const [deployModalOpen, setDeployModalOpen] = useState(false);
  const [detailDeployId, setDetailDeployId] = useState<string | null>(null);

  const deploysQuery = useDeploys({ limit: 100 });
  const domainsQuery = useDomains();
  const items = deploysQuery.data?.deploys ?? EMPTY_DEPLOYS;
  const filtered = useMemo(() => applyFilter(items, filter), [items, filter]);

  // The filter still selects deploys — "запущенные" means a project with a
  // running build, not a project whose newest build happens to be running.
  const projects = useMemo(() => groupDeploysByProject(filtered), [filtered]);
  // Rows predating Task 16a carry no project and cannot be grouped; they keep
  // the old per-deploy card rather than disappearing from the list.
  const ungrouped = useMemo(() => filtered.filter((d) => !d.project_id), [filtered]);

  const domainsByProject = useMemo(() => {
    const domains = (domainsQuery.data?.domains ?? []).filter((d) => d.status === 'verified');
    const map = new Map<string, CustomDomain[]>();
    for (const domain of domains) {
      const list = map.get(domain.project_id);
      if (list) list.push(domain);
      else map.set(domain.project_id, [domain]);
    }
    return map;
  }, [domainsQuery.data]);

  return (
    <div className={`${styles.root} flex flex-col gap-6 max-w-7xl mx-auto`}>
      <header className="flex items-start justify-between gap-4 flex-wrap">
        <div>
          <h1 className="text-2xl font-semibold text-zinc-900 tracking-tight">Проекты</h1>
          <p className="text-sm text-zinc-500 mt-1">
            Один проект — одна карточка; сборки и откаты внутри
          </p>
        </div>
        <Button
          variant="primary"
          iconLeft={<Plus size={16} />}
          onClick={() => setDeployModalOpen(true)}
        >
          Запустить проект
        </Button>
      </header>

      <Tabs
        items={FILTERS}
        value={filter}
        onChange={(v) => setFilter(v as FilterKey)}
        ariaLabel="Фильтр по статусу"
      />

      {deploysQuery.isLoading && (
        <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
          {Array.from({ length: 6 }).map((_, i) => (
            <div
              key={i}
              className="bg-white border border-zinc-200 rounded-xl p-5 flex flex-col gap-3"
            >
              <div className="flex items-center justify-between gap-2">
                <Skeleton className="h-5 w-32" />
                <Skeleton className="h-6 w-20 rounded-full" />
              </div>
              <Skeleton className="h-4 w-48" />
              <Skeleton className="h-4 w-40" />
              <div className="pt-3 border-t border-zinc-100 flex justify-end gap-2">
                <Skeleton className="h-8 w-20" />
                <Skeleton className="h-8 w-20" />
              </div>
            </div>
          ))}
        </div>
      )}

      {deploysQuery.isError && !deploysQuery.isLoading && (
        <div className="rounded-lg border border-red-200 bg-red-50 p-4 flex items-center justify-between gap-4">
          <div className="text-sm text-red-800">Не удалось загрузить список проектов.</div>
          <Button
            variant="secondary"
            size="sm"
            iconLeft={<RefreshCw size={14} />}
            onClick={() => deploysQuery.refetch()}
          >
            Повторить
          </Button>
        </div>
      )}

      {!deploysQuery.isLoading && !deploysQuery.isError && items.length === 0 && (
        <EmptyState
          icon={<FolderOpen className="w-16 h-16" strokeWidth={1.5} />}
          title="У вас пока нет проектов"
          description="Запустите первый проект — просто вставьте URL репозитория, остальное мы сделаем сами."
          action={
            <Button
              variant="primary"
              iconLeft={<Plus size={16} />}
              onClick={() => setDeployModalOpen(true)}
            >
              Запустить первый проект
            </Button>
          }
        />
      )}

      {!deploysQuery.isLoading &&
        !deploysQuery.isError &&
        items.length > 0 &&
        filtered.length === 0 && <EmptyState title="Нет проектов в этой категории" />}

      {!deploysQuery.isLoading && projects.length > 0 && (
        <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
          {projects.map((project) => (
            <ProjectSummaryCard
              key={project.projectId}
              project={project}
              domains={domainsByProject.get(project.projectId) ?? []}
            />
          ))}
        </div>
      )}

      {!deploysQuery.isLoading && ungrouped.length > 0 && (
        <section className="flex flex-col gap-3">
          <h2 className="text-sm font-medium text-zinc-900">Без проекта</h2>
          <p className="text-xs text-zinc-500">
            Эти деплои созданы до появления проектов, поэтому к ним нельзя привязать домен.
          </p>
          <ProjectGrid deploys={ungrouped} onOpen={(id) => setDetailDeployId(id)} />
        </section>
      )}

      <DeployModal isOpen={deployModalOpen} onClose={() => setDeployModalOpen(false)} />
      <ProjectDetailModal
        isOpen={detailDeployId !== null}
        deployId={detailDeployId}
        onClose={() => setDetailDeployId(null)}
      />
    </div>
  );
}

export default ProjectsPage;
