import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { DeploySummary } from '@/entities/deploy';
import { startDeployMessage } from '@/features/control-deploy';
import { api, ApiError } from '@/shared/api/http';
import { ToastProvider } from '@/shared/ui/toast';
import { ProjectCard } from '@/widgets/project-overview';

function deploy(overrides: Partial<DeploySummary> = {}): DeploySummary {
  return {
    id: 'd1e2f3a4-5b6c-4d7e-8f90-1a2b3c4d5e6f',
    project_id: '2b1b7f26-2d4e-4c2f-9c9a-1c2d3e4f5a6b',
    repo_url: 'https://github.com/acme/demo',
    branch: 'main',
    commit_sha: null,
    status: 'running',
    endpoint_url: 'https://proj.example.test',
    subdomain: 'proj',
    created_at: '2026-08-30T10:00:00Z',
    stopped_at: null,
    ...overrides,
  } as DeploySummary;
}

function renderCard(d: DeploySummary) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <ToastProvider>
        <MemoryRouter>
          <ProjectCard deploy={d} onOpen={() => {}} />
        </MemoryRouter>
      </ToastProvider>
    </QueryClientProvider>,
  );
}

describe('deploy controls on the project card', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  /**
   * The regression this file exists for. "Остановить" and "Удалить" were the
   * same mutation wired to two buttons, so stopping a deploy destroyed it —
   * and once image GC landed, destroyed its image too. They are different
   * operations now and the stop path has to keep the deploy startable.
   */
  it('stops through its own endpoint, without deleting anything', async () => {
    const post = vi.spyOn(api, 'post').mockResolvedValue({ status: 'stopped' });
    const del = vi.spyOn(api, 'delete').mockResolvedValue(undefined);
    const d = deploy({ status: 'running' });
    renderCard(d);

    await userEvent.click(screen.getByRole('button', { name: 'Остановить' }));

    await waitFor(() => expect(post).toHaveBeenCalledWith(`/api/v1/deploys/${d.id}/stop`));
    // The whole point: DELETE marks the deploy deleted and releases its image,
    // so a stop that went through it would destroy the artifact Start needs.
    expect(del).not.toHaveBeenCalled();
  });

  it('deletes through DELETE, which is the call that releases the image', async () => {
    const del = vi.spyOn(api, 'delete').mockResolvedValue(undefined);
    const d = deploy({ status: 'running' });
    renderCard(d);

    await userEvent.click(screen.getByRole('button', { name: 'Удалить' }));
    await waitFor(() => expect(del).toHaveBeenCalledWith(`/api/v1/deploys/${d.id}`));
  });

  /** A stopped deploy kept its image, so starting it is a container run. */
  it('offers Запустить for a stopped deploy and calls the start endpoint', async () => {
    const post = vi.spyOn(api, 'post').mockResolvedValue({
      status: 'running',
      container_id: 'c1',
      endpoint_url: 'https://proj.example.test',
    });
    const d = deploy({ status: 'stopped' });
    renderCard(d);

    await userEvent.click(screen.getByRole('button', { name: 'Запустить' }));
    await waitFor(() => expect(post).toHaveBeenCalledWith(`/api/v1/deploys/${d.id}/start`));
  });

  /**
   * A failed build produced no working image — the sweep reclaims those — so
   * there is nothing to start. Offering the button would be a guaranteed
   * error, which is what the old "Перезапустить" was: it called an endpoint
   * this backend has never had.
   */
  it('offers no start for a failed deploy', () => {
    renderCard(deploy({ status: 'failed' }));

    expect(screen.queryByRole('button', { name: 'Запустить' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Перезапустить' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Удалить' })).toBeInTheDocument();
  });

  it('offers nothing actionable while a build is in flight', () => {
    renderCard(deploy({ status: 'building' }));

    expect(screen.queryByRole('button', { name: 'Удалить' })).toBeNull();
    expect(screen.getByRole('button', { name: 'В процессе' })).toBeDisabled();
  });
});

describe('startDeployMessage', () => {
  /**
   * image_reclaimed is the one that matters: it means the artifact is gone and
   * the answer is a new deploy, not a retry. A generic failure message would
   * leave the operator clicking a button that can never work.
   */
  it.each([
    ['image_reclaimed', /задеплойте проект заново/],
    ['not_stopped', /не остановлен/],
    ['start_failed', /Откройте логи/],
  ])('explains %s specifically', (code, expected) => {
    expect(startDeployMessage(new ApiError(409, { error: code }, 'failed'))).toMatch(expected);
  });

  it('falls back for an unknown code', () => {
    expect(startDeployMessage(new ApiError(500, { error: 'boom' }, 'failed'))).toBe(
      'Не удалось запустить деплой',
    );
  });
});
