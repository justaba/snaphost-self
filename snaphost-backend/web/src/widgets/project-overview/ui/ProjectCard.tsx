import { type MouseEvent } from 'react';
import { Link } from 'react-router-dom';
import { ExternalLink, GitBranch, Layers } from 'lucide-react';
import {
  DeployStatusBadge as ProjectStatusBadge,
  isTransitionalStatus,
  useUptime,
  type DeploySummary,
} from '@/entities/deploy';
import { useDeleteDeploy } from '@/features/delete-deploy';
import { useRestartDeploy } from '@/features/monitor-deploy';
import { Button } from '@/shared/ui/button';
import { useToast } from '@/shared/ui/toast';
import styles from './ProjectCard.module.css';

export interface ProjectCardProps {
  deploy: DeploySummary;
  onOpen: (id: string) => void;
}

function parseRepoName(repoUrl: string): string {
  try {
    const u = new URL(repoUrl);
    const segments = u.pathname
      .replace(/^\/+|\.git$/g, '')
      .split('/')
      .filter(Boolean);
    if (segments.length >= 2) return `${segments[0]}/${segments[1]}`;
    return segments.join('/') || repoUrl;
  } catch {
    return repoUrl;
  }
}

function ProjectCard({ deploy, onOpen }: ProjectCardProps) {
  const { uptime, isLive } = useUptime(deploy.created_at, deploy.status, deploy.stopped_at);
  const deleteMut = useDeleteDeploy();
  const restartMut = useRestartDeploy();
  const { toast } = useToast();
  const repoName = parseRepoName(deploy.repo_url);
  const inTransition = isTransitionalStatus(deploy.status);

  const stopPropagation = (e: MouseEvent) => e.stopPropagation();

  const handleDelete = (e: MouseEvent) => {
    e.stopPropagation();
    if (deleteMut.isPending) return;
    deleteMut.mutate(deploy.id, {
      onSuccess: () => toast('Деплой удалён', 'success'),
      onError: (err) => toast(err.message || 'Не удалось удалить деплой', 'error'),
    });
  };

  const handleRestart = (e: MouseEvent) => {
    e.stopPropagation();
    if (restartMut.isPending) return;
    restartMut.mutate(deploy.id, {
      onSuccess: () => toast('Деплой перезапущен', 'success'),
      onError: (err) => toast(err.message || 'Не удалось перезапустить', 'error'),
    });
  };

  const renderActions = () => {
    if (deploy.status === 'running') {
      return (
        <>
          <Button variant="ghost" size="sm" onClick={handleDelete} loading={deleteMut.isPending}>
            Остановить
          </Button>
          <Button variant="danger" size="sm" onClick={handleDelete} loading={deleteMut.isPending}>
            Удалить
          </Button>
        </>
      );
    }
    if (deploy.status === 'failed' || deploy.status === 'stopped') {
      return (
        <>
          <Button
            variant="primary"
            size="sm"
            onClick={handleRestart}
            loading={restartMut.isPending}
          >
            Перезапустить
          </Button>
          <Button variant="danger" size="sm" onClick={handleDelete} loading={deleteMut.isPending}>
            Удалить
          </Button>
        </>
      );
    }
    return (
      <Button variant="ghost" size="sm" disabled>
        Отменить
      </Button>
    );
  };

  return (
    <article
      role="button"
      tabIndex={0}
      onClick={() => onOpen(deploy.id)}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          onOpen(deploy.id);
        }
      }}
      className={styles.root}
    >
      <div className="flex items-start justify-between gap-3 mb-3">
        <div className="min-w-0">
          {deploy.project_id ? (
            <Link
              to={`/dashboard/projects/${deploy.project_id}`}
              onClick={stopPropagation}
              className="text-base font-medium text-zinc-900 truncate hover:underline underline-offset-2 block"
            >
              {repoName}
            </Link>
          ) : (
            <h3 className="text-base font-medium text-zinc-900 truncate">{repoName}</h3>
          )}
          <div className="mt-1 inline-flex items-center gap-1 text-xs text-zinc-500">
            <GitBranch size={12} />
            <span className="font-mono">{deploy.branch}</span>
          </div>
        </div>
        <div className="shrink-0 flex items-center gap-2">
          <ProjectStatusBadge status={deploy.status} />
          {inTransition && (
            <span className="w-2 h-2 rounded-full bg-amber-400 animate-pulse" aria-hidden="true" />
          )}
        </div>
      </div>

      {deploy.endpoint_url ? (
        <a
          href={deploy.endpoint_url}
          target="_blank"
          rel="noreferrer noopener"
          onClick={stopPropagation}
          className="inline-flex items-center gap-1.5 text-sm text-zinc-700 hover:text-zinc-900 font-mono truncate max-w-full mb-3"
        >
          <ExternalLink size={14} className="shrink-0" />
          <span className="truncate">{deploy.endpoint_url}</span>
        </a>
      ) : (
        <div className="text-sm text-zinc-400 mb-3">URL появится после запуска</div>
      )}

      <div className="flex items-center justify-between text-xs text-zinc-500 mb-4">
        <span>
          {isLive ? 'Работает: ' : 'Время: '}
          <span className="text-zinc-700 font-medium">{uptime}</span>
        </span>
        {deploy.commit_sha && <span className="font-mono">{deploy.commit_sha.slice(0, 7)}</span>}
      </div>

      <div className="flex items-center justify-between gap-2 pt-3 border-t border-zinc-100">
        {deploy.project_id ? (
          <Link
            to={`/dashboard/projects/${deploy.project_id}`}
            onClick={stopPropagation}
            className="inline-flex items-center gap-1.5 h-8 px-3 rounded-lg text-sm font-medium text-zinc-700 bg-zinc-100 hover:bg-zinc-200 transition-colors"
          >
            <Layers size={15} />
            Проект
          </Link>
        ) : (
          <span />
        )}
        <div className="flex items-center gap-2">{renderActions()}</div>
      </div>
    </article>
  );
}

export default ProjectCard;
