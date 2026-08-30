import type { ChangePasswordData, Session, SignInData } from '../model/types';

/**
 * What signing in and out means here.
 *
 * The interface it replaced had nine methods, because Supabase supplied nine:
 * sign-up, OAuth against two providers, a refresh, a password-reset email, and
 * a listener for auth state changing in another tab. None of them has anything
 * behind it now. This platform has one operator whose account exists before the
 * first request, no identity provider to redirect to, and a session the server
 * renews on its own — so there is nothing to refresh and nothing to subscribe
 * to.
 */
export interface AuthProvider {
  signIn(data: SignInData): Promise<Session>;
  signOut(): Promise<void>;
  /** Returns null when there is no valid session cookie. */
  getSession(): Promise<Session | null>;
  changePassword(data: ChangePasswordData): Promise<void>;
}
