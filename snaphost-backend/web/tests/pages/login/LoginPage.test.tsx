import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { configureStore } from '@reduxjs/toolkit';
import { Provider } from 'react-redux';
import { MemoryRouter } from 'react-router-dom';

import { auth, authReducer } from '@/entities/session';
import { LoginPage } from '@/pages/login';

function renderPage() {
  const store = configureStore({ reducer: { auth: authReducer } });
  render(
    <Provider store={store}>
      <MemoryRouter>
        <LoginPage />
      </MemoryRouter>
    </Provider>,
  );
}

describe('first-entry screen', () => {
  beforeEach(() => vi.restoreAllMocks());
  afterEach(() => {
    window.history.replaceState(null, '', '/');
  });

  it('shows setup when required and removes the private fragment from the URL', async () => {
    window.history.replaceState(null, '', '/login#setup-token=private-test-token');
    vi.spyOn(auth, 'setupRequired').mockResolvedValue(true);
    renderPage();
    expect(await screen.findByLabelText('Повторите пароль')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Создать аккаунт' })).toBeInTheDocument();
    expect(window.location.hash).toBe('');
  });

  it('shows normal login for an already configured account', async () => {
    vi.spyOn(auth, 'setupRequired').mockResolvedValue(false);
    renderPage();
    expect(await screen.findByLabelText('Логин или email')).toBeInTheDocument();
    expect(screen.queryByLabelText('Повторите пароль')).not.toBeInTheDocument();
  });

  it('does not guess setup state when the server is unavailable', async () => {
    vi.spyOn(auth, 'setupRequired').mockRejectedValue(new Error('unavailable'));
    renderPage();
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent('Не удалось проверить настройку'),
    );
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });
});
