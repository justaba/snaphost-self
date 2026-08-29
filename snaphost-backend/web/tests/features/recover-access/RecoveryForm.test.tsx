import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { RecoveryForm } from '@/features/recover-access';

const { sendPasswordResetEmail } = vi.hoisted(() => ({
  sendPasswordResetEmail: vi.fn(),
}));

vi.mock('@/entities/session', () => ({
  auth: { sendPasswordResetEmail },
}));

describe('RecoveryForm', () => {
  beforeEach(() => {
    sendPasswordResetEmail.mockReset();
  });

  it('requests a one-time password recovery link', async () => {
    const user = userEvent.setup();
    sendPasswordResetEmail.mockResolvedValue(undefined);

    render(
      <MemoryRouter>
        <RecoveryForm />
      </MemoryRouter>,
    );

    await user.type(screen.getByLabelText('Email аккаунта'), 'owner@example.com');
    await user.click(screen.getByRole('button', { name: /получить ссылку/i }));

    await waitFor(() => {
      expect(sendPasswordResetEmail).toHaveBeenCalledWith(
        'owner@example.com',
        'http://localhost:3000/reset-password',
      );
    });
    expect(screen.getByRole('status')).toHaveTextContent('мы отправили');
  });

  it('does not submit an invalid email', async () => {
    const user = userEvent.setup();

    render(
      <MemoryRouter>
        <RecoveryForm />
      </MemoryRouter>,
    );

    await user.type(screen.getByLabelText('Email аккаунта'), 'wrong-email');
    await user.click(screen.getByRole('button', { name: /получить ссылку/i }));

    expect(await screen.findByText('Введите корректный email.')).toBeInTheDocument();
    expect(sendPasswordResetEmail).not.toHaveBeenCalled();
  });
});
