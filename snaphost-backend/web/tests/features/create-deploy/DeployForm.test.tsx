import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import { DeployForm } from '@/features/create-deploy';

describe('DeployForm', () => {
  it('submits validated repository data', async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);

    render(
      <DeployForm
        submitting={false}
        error={null}
        onSubmit={onSubmit}
        onCancel={vi.fn()}
      />,
    );

    await user.type(screen.getByLabelText('URL репозитория'), 'https://github.com/acme/app');
    await user.click(screen.getByRole('button', { name: 'Деплой' }));

    expect(onSubmit).toHaveBeenCalledWith({
      repo_url: 'https://github.com/acme/app',
      branch: 'main',
      env: [],
    });
  });

  it('shows validation feedback for an unsupported repository', async () => {
    const user = userEvent.setup();

    render(
      <DeployForm
        submitting={false}
        error={null}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    await user.type(screen.getByLabelText('URL репозитория'), 'https://example.com/acme/app');
    await user.click(screen.getByRole('button', { name: 'Деплой' }));

    expect(
      await screen.findByText(
        'Поддерживаются только https-адреса github.com, gitlab.com, bitbucket.org',
      ),
    ).toBeInTheDocument();
  });
});
