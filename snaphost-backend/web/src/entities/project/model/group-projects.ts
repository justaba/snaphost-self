import type { DeploySummary } from '@/entities/deploy/@x/project';

export interface ProjectGroup {
  projectId: string;
  /** Readable name derived from the source; projects have no user-set name yet. */
  label: string;
  /** Deploys of this project, newest first — each one is an immutable build
   *  with its own permanent URL, and therefore a rollback target. */
  deploys: DeploySummary[];
}

/** Human label for one build's source. */
export function deployLabel(deploy: DeploySummary): string {
  if (!deploy.repo_url) return 'Загруженный архив';
  const trimmed = deploy.repo_url.replace(/\/+$/, '').replace(/\.git$/, '');
  const name = trimmed.slice(trimmed.lastIndexOf('/') + 1);
  return name || deploy.repo_url;
}

/**
 * Groups deploys into their projects. The deploy list is the only source we
 * have for project membership today; rows created before projects existed
 * carry no project_id and are skipped rather than merged into a fake group —
 * a domain cannot be attached to them anyway.
 */
export function groupDeploysByProject(deploys: DeploySummary[]): ProjectGroup[] {
  const groups = new Map<string, ProjectGroup>();

  for (const deploy of deploys) {
    if (!deploy.project_id) continue;
    const existing = groups.get(deploy.project_id);
    if (existing) {
      existing.deploys.push(deploy);
      continue;
    }
    groups.set(deploy.project_id, {
      projectId: deploy.project_id,
      label: deployLabel(deploy),
      deploys: [deploy],
    });
  }

  for (const group of groups.values()) {
    group.deploys.sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at));
    group.label = deployLabel(group.deploys[0]);
  }

  return Array.from(groups.values()).sort(
    (a, b) => Date.parse(b.deploys[0].created_at) - Date.parse(a.deploys[0].created_at),
  );
}

export function findProject(groups: ProjectGroup[], projectId: string): ProjectGroup | undefined {
  return groups.find((g) => g.projectId === projectId);
}
