import type { AuthProvider } from './provider';
import type { AuthError, ChangePasswordData, Session, SignInData, User } from '../model/types';

/**
 * Talks to this platform's own /api/v1/auth endpoints.
 *
 * Deliberately not routed through shared/api/http: that client exists to add
 * credentials and react to a 401 by signing out, and these four calls are the
 * ones that establish and end the credential in the first place. Sending a
 * failed login through a layer that responds to 401 by logging you out would
 * be a loop with one useful iteration.
 */

interface MeResponse {
  user_id: string;
  email: string;
  role: string;
}

const base = import.meta.env.VITE_API_URL ?? '';

function toUser(body: MeResponse): User {
  return {
    id: body.user_id,
    email: body.email,
    role: body.role === 'admin' ? 'admin' : 'user',
  };
}

function authError(code: AuthError['code'], message: string): AuthError {
  return { code, message };
}

async function readError(response: Response, fallback: string): Promise<AuthError> {
  let body: { error?: string; message?: string } = {};
  try {
    body = (await response.json()) as typeof body;
  } catch {
    // A body that is not JSON tells us nothing the status has not already.
  }
  const message = body.message ?? fallback;

  switch (response.status) {
    case 401:
      return authError('invalid_credentials', message);
    case 429:
      return authError('rate_limited', message);
    case 400:
      return authError(body.error === 'weak_password' ? 'weak_password' : 'unknown', message);
    default:
      return authError('unknown', message);
  }
}

export class SessionAuthProvider implements AuthProvider {
  async signIn(data: SignInData): Promise<Session> {
    const response = await fetch(`${base}/api/v1/auth/login`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      // Same-origin in production, because one binary serves both. Explicit so
      // that a development server on another port still sends the cookie.
      credentials: 'include',
      body: JSON.stringify({ email: data.email, password: data.password }),
    });

    if (!response.ok) {
      throw await readError(response, 'Не удалось войти.');
    }
    return { user: toUser((await response.json()) as MeResponse) };
  }

  async signOut(): Promise<void> {
    await fetch(`${base}/api/v1/auth/logout`, {
      method: 'POST',
      credentials: 'include',
    });
  }

  async getSession(): Promise<Session | null> {
    const response = await fetch(`${base}/api/v1/auth/me`, { credentials: 'include' });
    if (response.status === 401) {
      return null;
    }
    if (!response.ok) {
      throw await readError(response, 'Не удалось проверить сессию.');
    }
    return { user: toUser((await response.json()) as MeResponse) };
  }

  async changePassword(data: ChangePasswordData): Promise<void> {
    const response = await fetch(`${base}/api/v1/auth/password`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify({
        current_password: data.currentPassword,
        new_password: data.newPassword,
      }),
    });

    if (!response.ok) {
      throw await readError(response, 'Не удалось сменить пароль.');
    }
  }
}
