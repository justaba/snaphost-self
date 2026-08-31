import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { ProjectSummary } from '@/entities/project';
import { DeleteProjectDialog, deleteProjectMessage } from '@/features/delete-project';
import { api, ApiError } from '@/shared/api/http';
import { ToastProvider } from '@/shared/ui/toast';

const project: ProjectSummary = {
  id: '2b1b7f26-2d4e-4c2f-9c9a-1c2d3e4f5a6b',
  user_id: 'b6a5d4c3-2e1f-4a3b-8c7d-6e5f4a3b2c1d',
  slug: 'demo-app',
  source_key: 'git:github.com/acme/demo#main',
  deploys_count: 3,
  running_count: 1,
  stopped_count: 1,
  domains_count: 2,
  created_at: '2026-08-01T10:00:00Z',
};

function renderDialog(onClose = vi.fn()) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <ToastProvider>
        <DeleteProjectDialog project={project} isOpen onClose={onClose} />
      </ToastProvider>
    </QueryClientProvider>,
  );
  return { onClose };
}

function confirmButton() {
  return screen.getByRole('button', { name: 'Удалить проект' });
}

describe('DeleteProjectDialog', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  /**
   * The deletion stops containers, removes Docker images and hard-deletes the
   * rows. None of it is restorable from the panel, and the button sits in a
   * table where the rows differ only by slug — so a misclick must not be one
   * click away from destroying the wrong project.
   */
  it('will not submit until the slug is typed exactly', async () => {
    const del = vi.spyOn(api, 'delete');
    renderDialog();

    expect(confirmButton()).toBeDisabled();

    const field = screen.getByLabelText(/для подтверждения/);
    await userEvent.type(field, 'demo-ap');
    expect(confirmButton()).toBeDisabled();

    await userEvent.type(field, 'p');
    await waitFor(() => expect(confirmButton()).toBeEnabled());
    expect(del).not.toHaveBeenCalled();
  });

  // The path is on the ordinary user surface now. /api/v1/admin is gone
  // entirely, so a request there would 403 at the Casbin layer.
  it('deletes through the project endpoint and closes', async () => {
    const del = vi.spyOn(api, 'delete').mockResolvedValue(undefined);
    const { onClose } = renderDialog();

    await userEvent.type(screen.getByLabelText(/для подтверждения/), project.slug);
    await userEvent.click(confirmButton());

    await waitFor(() => expect(del).toHaveBeenCalledWith(`/api/v1/projects/${project.id}`));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  // Deleting a project with a live site is a different act from reclaiming
  // disk, and the dialog has to say which one this is.
  it('says when a running site goes with it', () => {
    renderDialog();
    expect(screen.getByText(/Сейчас работает сборок: 1/)).toBeInTheDocument();
  });

  /**
   * A refused deletion has to stay open and say why. The server refuses while
   * a build is in flight and when Docker cleanup fails, and in both cases the
   * project is still there — closing the dialog would read as success.
   */
  it('keeps the dialog open and reports why the server refused', async () => {
    vi.spyOn(api, 'delete').mockRejectedValue(
      new ApiError(409, { error: 'project_busy' }, 'Request failed: 409'),
    );
    const { onClose } = renderDialog();

    await userEvent.type(screen.getByLabelText(/для подтверждения/), project.slug);
    await userEvent.click(confirmButton());

    expect(await screen.findByText(/Идёт сборка или запуск/)).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
  });

  it('warns that images and domains go too', () => {
    renderDialog();
    expect(screen.getByText(/удалены Docker-образы/)).toBeInTheDocument();
    expect(screen.getByText(/привязанные домены/)).toBeInTheDocument();
  });
});

describe('deleteProjectMessage', () => {
  /**
   * Each refusal tells the operator to do something different — wait for the
   * build, fix the daemon, or nothing at all because it is already gone. A
   * single "не удалось удалить" for all of them loses that.
   */
  it.each([
    ['project_busy', /Идёт сборка/],
    ['cleanup_failed', /Ничего не удалено/],
    ['runtime_unavailable', /Runtime не настроен/],
    ['project_not_found', /уже удалён/],
  ])('explains %s specifically', (code, expected) => {
    const message = deleteProjectMessage(new ApiError(409, { error: code }, 'Request failed'));
    expect(message).toMatch(expected);
  });

  it('falls back for an error with no code', () => {
    expect(deleteProjectMessage(new Error('network down'))).toBe('network down');
    expect(deleteProjectMessage(new ApiError(500, null, 'Request failed: 500'))).toBe(
      'Не удалось удалить проект',
    );
  });
});
