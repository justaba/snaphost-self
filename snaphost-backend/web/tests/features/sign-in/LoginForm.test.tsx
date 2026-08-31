import { readFileSync } from 'node:fs';

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

/**
 * The regression this pins. The form was written against `auth-form`,
 * `auth-input`, `auth-submit` and friends — global classes belonging to the
 * marketing stylesheet, which left with the marketing site. Nothing defined
 * them any more, so the first screen every operator sees rendered as unstyled
 * browser defaults, and no test noticed because every assertion was about
 * behaviour.
 */
describe('LoginForm styling', () => {
  it('uses no class the stylesheet does not define', () => {
    // Comments are stripped first: the file explains this history in prose and
    // names the dead classes to do it, which is worth keeping.
    const code = readFileSync('src/features/sign-in/ui/LoginForm.tsx', 'utf8')
      .replace(/\/\*[\s\S]*?\*\//g, '')
      .replace(/\/\/.*$/gm, '');

    expect(code).not.toMatch(/auth-(form|input|submit|message|field-error)/);
  });

  it('renders a styled submit control rather than a bare button', () => {
    renderForm();

    const submit = screen.getByRole('button', { name: /Войти/ });
    // The shared Button always carries a background utility; a bare <button>
    // carries none. This is the cheapest assertion that separates the two.
    expect(submit.className).toMatch(/bg-/);
  });

  it('keeps both fields labelled after the rewrite', () => {
    renderForm();

    expect(screen.getByLabelText('Email')).toBeInTheDocument();
    expect(screen.getByLabelText('Пароль')).toBeInTheDocument();
  });
});
