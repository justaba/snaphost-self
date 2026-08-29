import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ResetPasswordForm } from '@/features/reset-password';

const { updatePassword } = vi.hoisted(() => ({
  updatePassword: vi.fn(),
}));

vi.mock('@/entities/session', () => ({
  auth: { updatePassword },
}));

describe('ResetPasswordForm', () => {
  beforeEach(() => {
    updatePassword.mockReset();
  });

  it('updates the password and opens the dashboard', async () => {
    const user = userEvent.setup();
    updatePassword.mockResolvedValue(undefined);

    render(
      <MemoryRouter initialEntries={['/reset-password']}>
        <Routes>
          <Route path="/reset-password" element={<ResetPasswordForm />} />
          <Route path="/dashboard" element={<div>Dashboard opened</div>} />
        </Routes>
      </MemoryRouter>,
    );

    await user.type(screen.getByLabelText('Новый пароль'), 'new-password');
    await user.type(screen.getByLabelText('Повторите пароль'), 'new-password');
    await user.click(screen.getByRole('button', { name: /сохранить пароль/i }));

    await waitFor(() => expect(updatePassword).toHaveBeenCalledWith('new-password'));
    expect(await screen.findByText('Dashboard opened')).toBeInTheDocument();
  });

  it('shows a validation error when passwords differ', async () => {
    const user = userEvent.setup();

    render(
      <MemoryRouter>
        <ResetPasswordForm />
      </MemoryRouter>,
    );

    await user.type(screen.getByLabelText('Новый пароль'), 'new-password');
    await user.type(screen.getByLabelText('Повторите пароль'), 'other-password');
    await user.click(screen.getByRole('button', { name: /сохранить пароль/i }));

    expect(await screen.findByText('Пароли не совпадают.')).toBeInTheDocument();
    expect(updatePassword).not.toHaveBeenCalled();
  });
});
