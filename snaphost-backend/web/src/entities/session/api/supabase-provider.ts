import type {
  AuthError as SupabaseAuthError,
  Session as SupabaseSession,
  User as SupabaseUser,
} from '@supabase/supabase-js';
import { jwtDecode } from 'jwt-decode';

import { supabase } from '@/shared/api/supabase';
import type { AuthProvider } from './provider';
import type {
  AuthError,
  AuthStateListener,
  OAuthProvider,
  Session,
  SignInData,
  SignUpData,
  Unsubscribe,
  User,
} from '../model/types';

interface SnaphostJwtClaims {
  snaphost_role?: unknown;
}

function isUserRole(value: unknown): value is User['role'] {
  return value === 'user' || value === 'pro' || value === 'admin';
}

function decodeRole(accessToken: string): User['role'] {
  try {
    const claims = jwtDecode<SnaphostJwtClaims>(accessToken);
    if (claims && isUserRole(claims.snaphost_role)) {
      return claims.snaphost_role;
    }
    return 'user';
  } catch {
    return 'user';
  }
}

function mapUser(supaUser: SupabaseUser, accessToken: string): User {
  const metadata = (supaUser.user_metadata ?? {}) as Record<string, unknown>;
  const displayName = typeof metadata.display_name === 'string' ? metadata.display_name : null;
  const avatarUrl = typeof metadata.avatar_url === 'string' ? metadata.avatar_url : null;

  return {
    id: supaUser.id,
    email: supaUser.email ?? '',
    displayName,
    avatarUrl,
    role: decodeRole(accessToken),
  };
}

function mapSession(supaSession: SupabaseSession | null): Session | null {
  if (!supaSession) return null;
  const expiresAt =
    supaSession.expires_at ?? Math.floor(Date.now() / 1000) + (supaSession.expires_in ?? 0);
  return {
    accessToken: supaSession.access_token,
    refreshToken: supaSession.refresh_token,
    expiresAt,
    user: mapUser(supaSession.user, supaSession.access_token),
  };
}

function mapError(err: unknown): AuthError {
  if (err && typeof err === 'object') {
    const maybe = err as Partial<SupabaseAuthError> & { status?: number; code?: string };
    const raw = (maybe.message ?? '').toLowerCase();
    const code = (maybe.code ?? '').toLowerCase();
    const status = maybe.status;

    if (code === 'invalid_credentials' || raw.includes('invalid login credentials')) {
      return { code: 'invalid_credentials', message: 'Invalid email or password.' };
    }
    if (code === 'email_not_confirmed' || raw.includes('email not confirmed')) {
      return {
        code: 'email_not_confirmed',
        message: 'Please confirm your email address before signing in.',
      };
    }
    if (
      code === 'user_already_exists' ||
      raw.includes('already registered') ||
      raw.includes('user already registered')
    ) {
      return { code: 'user_already_exists', message: 'An account with this email already exists.' };
    }
    if (
      code === 'weak_password' ||
      raw.includes('password should be') ||
      raw.includes('weak password')
    ) {
      return {
        code: 'weak_password',
        message: 'Password is too weak. Please choose a stronger one.',
      };
    }
    if (status === 429 || code === 'over_request_rate_limit' || raw.includes('rate limit')) {
      return { code: 'rate_limited', message: 'Too many attempts. Please try again later.' };
    }
    if (raw.includes('network') || raw.includes('fetch')) {
      return {
        code: 'network_error',
        message: 'Network error. Please check your connection and try again.',
      };
    }
    if (typeof maybe.message === 'string' && maybe.message.length > 0) {
      return { code: 'unknown', message: maybe.message };
    }
  }
  return { code: 'unknown', message: 'An unexpected error occurred.' };
}

export class SupabaseAuthProvider implements AuthProvider {
  async signUp(data: SignUpData): Promise<Session | null> {
    try {
      const { data: result, error } = await supabase.auth.signUp({
        email: data.email,
        password: data.password,
        options: {
          emailRedirectTo: `${window.location.origin}/auth/callback`,
          data: {
            display_name: data.displayName,
            github_username: data.githubUsername,
            offer_accepted: data.offerAccepted,
            acceptable_use_accepted: data.acceptableUseAccepted,
            personal_data_consent: data.personalDataConsent,
            age_confirmed: data.ageConfirmed,
            legal_version: data.legalVersion,
            client_accepted_at: data.acceptedAt,
          },
        },
      });
      if (error) throw error;
      if (result.session) {
        return mapSession(result.session);
      }
      if (result.user) {
        return null;
      }
      throw mapError({ message: 'Sign up failed' });
    } catch (err) {
      throw mapError(err);
    }
  }

  async signIn(data: SignInData): Promise<Session> {
    try {
      const { data: result, error } = await supabase.auth.signInWithPassword({
        email: data.email,
        password: data.password,
      });
      if (error) throw error;
      const mapped = mapSession(result.session);
      if (!mapped) {
        throw { code: 'unknown', message: 'Sign in returned no session.' } as AuthError;
      }
      return mapped;
    } catch (err) {
      if (err && typeof err === 'object' && 'code' in err && 'message' in err) {
        throw err as AuthError;
      }
      throw mapError(err);
    }
  }

  async signInWithOAuth(provider: OAuthProvider, redirectTo: string): Promise<void> {
    try {
      const { error } = await supabase.auth.signInWithOAuth({
        provider,
        options: { redirectTo },
      });
      if (error) throw error;
    } catch (err) {
      throw mapError(err);
    }
  }

  async signOut(): Promise<void> {
    try {
      await supabase.auth.signOut();
    } catch {
      // Silently succeed; signing out should never surface errors to the UI.
    }
  }

  async getSession(): Promise<Session | null> {
    try {
      const { data, error } = await supabase.auth.getSession();
      if (error) throw error;
      return mapSession(data.session);
    } catch (err) {
      throw mapError(err);
    }
  }

  async refreshSession(): Promise<Session | null> {
    try {
      const { data, error } = await supabase.auth.refreshSession();
      if (error) throw error;
      return mapSession(data.session);
    } catch {
      return null;
    }
  }

  onAuthStateChange(listener: AuthStateListener): Unsubscribe {
    const { data } = supabase.auth.onAuthStateChange((_event, session) => {
      listener(mapSession(session));
    });
    return (): void => {
      data.subscription.unsubscribe();
    };
  }

  async sendPasswordResetEmail(email: string, redirectTo: string): Promise<void> {
    try {
      const { error } = await supabase.auth.resetPasswordForEmail(email, { redirectTo });
      if (error) throw error;
    } catch (err) {
      throw mapError(err);
    }
  }

  async updatePassword(newPassword: string): Promise<void> {
    try {
      const { error } = await supabase.auth.updateUser({ password: newPassword });
      if (error) throw error;
    } catch (err) {
      throw mapError(err);
    }
  }
}
