import { describe, expect, it } from 'vitest';

import type { DeploySummary } from '@/entities/deploy';
import { deployLabel, groupDeploysByProject } from '@/entities/project';

function summary(overrides: Partial<DeploySummary>): DeploySummary {
  return {
    id: 'deploy-1',
    project_id: 'project-1',
    repo_url: 'https://github.com/acme/app.git',
    branch: 'main',
    commit_sha: null,
    status: 'running',
    endpoint_url: null,
    subdomain: null,
    cost_vibecoins: 10,
    ttl_expires_at: null,
    created_at: '2026-08-15T10:00:00Z',
    stopped_at: null,
    ...overrides,
  };
}

describe('project grouping', () => {
  it('derives a readable repository label', () => {
    expect(deployLabel(summary({}))).toBe('app');
    expect(deployLabel(summary({ repo_url: '' }))).toBe('Загруженный архив');
  });

  it('groups builds by project, sorts newest first, and skips legacy rows', () => {
    const groups = groupDeploysByProject([
      summary({ id: 'old', created_at: '2026-08-14T10:00:00Z' }),
      summary({ id: 'new', created_at: '2026-08-15T10:00:00Z' }),
      summary({ id: 'legacy', project_id: null }),
    ]);

    expect(groups).toHaveLength(1);
    expect(groups[0].deploys.map(({ id }) => id)).toEqual(['new', 'old']);
  });
});
