import { describe, expect, it } from 'vitest';

import type { DeployDetail, DeployStatus } from '@/entities/deploy';
import { deriveDeploySteps } from '@/features/monitor-deploy';

function deploy(status: DeployStatus, saga: Partial<DeployDetail['saga']> = {}): DeployDetail {
  return {
    id: 'deploy-1',
    project_id: 'project-1',
    user_id: 'user-1',
    repo_url: 'https://github.com/acme/app',
    branch: 'main',
    commit_sha: null,
    status,
    image_ref: null,
    endpoint_url: null,
    subdomain: null,
    cost_vibecoins: 10,
    ttl_expires_at: null,
    failure_reason: status === 'failed' ? 'build failed' : null,
    saga: {
      current_step: status,
      coins_reserved: false,
      image_built: false,
      container_running: false,
      coins_committed: false,
      retry_count: 0,
      ...saga,
    },
    created_at: '2026-08-15T10:00:00Z',
    updated_at: '2026-08-15T10:00:00Z',
    stopped_at: null,
  };
}

describe('deriveDeploySteps', () => {
  it('marks the reserve stage active for a pending deploy', () => {
    const steps = deriveDeploySteps(deploy('pending'));

    expect(steps[0]).toMatchObject({ key: 'reserve', state: 'active' });
    expect(steps.slice(1).every((step) => step.state === 'pending')).toBe(true);
  });

  it('marks every stage done for a running deploy', () => {
    const steps = deriveDeploySteps(
      deploy('running', {
        coins_reserved: true,
        image_built: true,
        container_running: true,
        coins_committed: true,
      }),
    );

    expect(steps.every((step) => step.state === 'done')).toBe(true);
  });

  it('marks the first incomplete stage failed', () => {
    const steps = deriveDeploySteps(deploy('failed', { coins_reserved: true }));

    expect(steps[0].state).toBe('done');
    expect(steps[1]).toMatchObject({ key: 'build', state: 'failed' });
  });
});
