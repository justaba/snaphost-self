import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { configureStore } from '@reduxjs/toolkit';
import { Provider } from 'react-redux';
import { MemoryRouter } from 'react-router-dom';

import { auth, authReducer } from '@/entities/session';
import { SetupForm } from '@/features/operator-setup';

function renderForm(token = 'private-test-token') {
  const store = configureStore({ reducer: { auth: authReducer } });
  render(
    <Provider store={store}>
      <MemoryRouter>
        <SetupForm token={token} />
      </MemoryRouter>
    </Provider>,
  );
  return store;
}

describe('operator setup', () => {
  beforeEach(() => vi.restoreAllMocks());

  it('asks for the installer link instead of accepting credentials without it', () => {
    renderForm('');
    expect(screen.getByText(/Откройте одноразовую ссылку/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Создать аккаунт' })).not.toBeInTheDocument();
  });

  it('submits chosen credentials and installer token, and keeps only identity in Redux', async () => {
    const complete = vi
      .spyOn(auth, 'completeSetup')
      .mockResolvedValue({ user: { id: 'u1', email: 'boris', role: 'admin' } });
    const store = renderForm();
    await userEvent.type(screen.getByLabelText('Логин'), 'boris');
    await userEvent.type(screen.getByLabelText('Пароль'), 'my chosen password');
    await userEvent.type(screen.getByLabelText('Повторите пароль'), 'my chosen password');
    await userEvent.click(screen.getByRole('button', { name: 'Создать аккаунт' }));
    expect(complete).toHaveBeenCalledWith({
      email: 'boris',
      password: 'my chosen password',
      token: 'private-test-token',
    });
    expect(store.getState().auth.status).toBe('authenticated');
    expect(JSON.stringify(store.getState())).not.toMatch(/private-test-token|my chosen password/);
  });

  it('refuses mismatching passwords before contacting the server', async () => {
    const complete = vi.spyOn(auth, 'completeSetup');
    renderForm();
    await userEvent.type(screen.getByLabelText('Логин'), 'boris');
    await userEvent.type(screen.getByLabelText('Пароль'), 'my chosen password');
    await userEvent.type(screen.getByLabelText('Повторите пароль'), 'a different password');
    await userEvent.click(screen.getByRole('button', { name: 'Создать аккаунт' }));
    expect(await screen.findByText('Пароли не совпадают.')).toBeInTheDocument();
    expect(complete).not.toHaveBeenCalled();
  });

  it('shows a rejected setup without establishing a session', async () => {
    vi.spyOn(auth, 'completeSetup').mockRejectedValue({ message: 'Ссылка настройки неверна.' });
    const store = renderForm();
    await userEvent.type(screen.getByLabelText('Логин'), 'boris');
    await userEvent.type(screen.getByLabelText('Пароль'), 'my chosen password');
    await userEvent.type(screen.getByLabelText('Повторите пароль'), 'my chosen password');
    await userEvent.click(screen.getByRole('button', { name: 'Создать аккаунт' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Ссылка настройки неверна.');
    expect(store.getState().auth.session).toBeNull();
  });
});
