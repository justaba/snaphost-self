import { describe, expect, it, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Provider } from 'react-redux';
import { MemoryRouter } from 'react-router-dom';
import { configureStore } from '@reduxjs/toolkit';

import { LoginForm } from '@/features/sign-in';
import { auth, authReducer } from '@/entities/session';

function renderForm() {
  const store = configureStore({ reducer: { auth: authReducer } });
  return render(
    <Provider store={store}>
      <MemoryRouter>
        <LoginForm />
      </MemoryRouter>
    </Provider>,
  );
}

describe('LoginForm', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  /**
   * The regression this file exists for. The schema used to be
   * `z.string().email()`, which rejects `operator@localhost` because the domain
   * has no dot — and that is the account the platform creates for itself on
   * first start. Submitting did nothing and the cursor jumped back to the email
   * field, so the panel refused the only credential it had ever issued.
   */
  it('accepts the address the platform creates for itself', async () => {
    const signIn = vi
      .spyOn(auth, 'signIn')
      .mockResolvedValue({ user: { id: 'u1', email: 'operator@localhost', role: 'admin' } });

    renderForm();
    await userEvent.type(screen.getByLabelText('Email'), 'operator@localhost');
    await userEvent.type(screen.getByLabelText('Пароль'), 'a-password');
    await userEvent.click(screen.getByRole('button', { name: /Войти/ }));

    expect(signIn).toHaveBeenCalledWith({
      email: 'operator@localhost',
      password: 'a-password',
    });
  });

  it('refuses an empty address without calling the server', async () => {
    const signIn = vi.spyOn(auth, 'signIn');

    renderForm();
    await userEvent.type(screen.getByLabelText('Пароль'), 'a-password');
    await userEvent.click(screen.getByRole('button', { name: /Войти/ }));

    expect(signIn).not.toHaveBeenCalled();
    expect(await screen.findByText('Введите email.')).toBeInTheDocument();
  });

  it('surfaces the reason a sign-in was refused', async () => {
    vi.spyOn(auth, 'signIn').mockRejectedValue({
      code: 'invalid_credentials',
      message: 'email или пароль неверны',
    });

    renderForm();
    await userEvent.type(screen.getByLabelText('Email'), 'operator@localhost');
    await userEvent.type(screen.getByLabelText('Пароль'), 'wrong');
    await userEvent.click(screen.getByRole('button', { name: /Войти/ }));

    expect(await screen.findByRole('alert')).toHaveTextContent('email или пароль неверны');
  });
});
