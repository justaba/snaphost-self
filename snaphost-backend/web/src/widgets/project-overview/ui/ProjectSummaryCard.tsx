import { Link } from 'react-router-dom';
import { ExternalLink, GitBranch, Globe, Layers, Trash2 } from 'lucide-react';

import {
  DeployStatusBadge as ProjectStatusBadge,
  useUptime,
  type DeploySummary,
} from '@/entities/deploy';
import type { CustomDomain } from '@/entities/domain';
import type { ProjectGroup } from '@/entities/project';
import styles from './ProjectSummaryCard.module.css';

export interface ProjectSummaryCardProps {
  project: ProjectGroup;
  /** Verified domains attached to this project, if any. */
  domains: CustomDomain[];
  /** Deleting the whole project, not one of its builds. Absent for the
   *  ungrouped legacy deploys, which have no project to delete. */
  onDelete?: (projectId: string) => void;
}

function parseRepoName(repoUrl: string): string {
  if (!repoUrl) return 'Загруженный архив';
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

/** Production uses the verified project domain. endpoint_url exists only for
 *  local development when DEV_DOMAIN_SUFFIX enables a Traefik route. */
function publicAddress(
  latest: DeploySummary,
  domains: CustomDomain[],
): { href: string; label: string; isDomain: boolean } | null {
  const serving = domains.find((d) => d.target_deploy_id !== null);
  if (serving) {
    return { href: `https://${serving.domain}`, label: serving.domain, isDomain: true };
  }
  if (latest.endpoint_url) {
    return { href: latest.endpoint_url, label: latest.endpoint_url, isDomain: false };
  }
  return null;
}

/**
 * One card per project, not per deploy. A project accumulates builds — with a
 * 24-hour TTL and per-project retention, several of them are alive at once, and
 * showing each as its own tile made one piece of work look like many.
 */
function ProjectSummaryCard({ project, domains, onDelete }: ProjectSummaryCardProps) {
  const latest = project.deploys[0];
  const running = project.deploys.filter((d) => d.status === 'running').length;
  const { uptime, isLive } = useUptime(latest.created_at, latest.status, latest.stopped_at);
  const address = publicAddress(latest, domains);

  return (
    <article className={styles.root}>
      <div className="flex items-start justify-between gap-3 mb-3">
        <div className="min-w-0">
          <Link
            to={`/dashboard/projects/${project.projectId}`}
            className="text-base font-medium text-zinc-900 truncate hover:underline underline-offset-2 block"
          >
            {parseRepoName(latest.repo_url)}
          </Link>
          <div className="mt-1 inline-flex items-center gap-1 text-xs text-zinc-500">
            <GitBranch size={12} />
            <span className="font-mono">{latest.branch}</span>
          </div>
        </div>
        <ProjectStatusBadge status={latest.status} />
      </div>

      {address ? (
        <a
          href={address.href}
          target="_blank"
          rel="noreferrer noopener"
          className="inline-flex items-center gap-1.5 text-sm text-zinc-700 hover:text-zinc-900 font-mono truncate max-w-full mb-3"
        >
          {address.isDomain ? (
            <Globe size={14} className="shrink-0 text-emerald-600" />
          ) : (
            <ExternalLink size={14} className="shrink-0" />
          )}
          <span className="truncate">{address.label}</span>
        </a>
      ) : (
        <div className="text-sm text-zinc-400 mb-3">URL появится после запуска</div>
      )}

      <div className="flex items-center justify-between text-xs text-zinc-500 mb-4">
        <span>
          {project.deploys.length} деплоев
          {running > 0 && ` · ${running} запущено`}
        </span>
        <span>
          {isLive ? 'Работает: ' : 'Обновлён: '}
          <span className="text-zinc-700 font-medium">{uptime}</span>
        </span>
      </div>

      <div className="mt-auto flex items-center justify-between gap-2 pt-3 border-t border-zinc-100">
        <span className="text-xs text-zinc-400 truncate">
          {latest.commit_sha ? (
            <span className="font-mono">{latest.commit_sha.slice(0, 7)}</span>
          ) : (
            'последняя сборка'
          )}
        </span>
        <div className="flex items-center gap-2">
          <Link
            to={`/dashboard/projects/${project.projectId}`}
            className="inline-flex items-center gap-1.5 h-8 px-3 rounded-lg text-sm font-medium text-zinc-700 bg-zinc-100 hover:bg-zinc-200 transition-colors"
          >
            <Layers size={15} />
            Открыть
          </Link>
          {onDelete && (
            <button
              type="button"
              onClick={() => onDelete(project.projectId)}
              aria-label={`Удалить проект ${parseRepoName(latest.repo_url)}`}
              title="Удалить проект со всеми сборками и доменами"
              className="inline-flex items-center justify-center h-8 w-8 rounded-lg text-red-600 hover:bg-red-50 transition-colors"
            >
              <Trash2 size={15} />
            </button>
          )}
        </div>
      </div>
    </article>
  );
}

export default ProjectSummaryCard;
