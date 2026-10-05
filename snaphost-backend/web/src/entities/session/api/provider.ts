import type { ChangePasswordData, Session, SignInData } from '../model/types';

/** Session authentication and the one-time creation of this operator account. */
export interface AuthProvider {
  setupRequired(): Promise<boolean>;
  completeSetup(data: SignInData & { token: string }): Promise<Session>;
  signIn(data: SignInData): Promise<Session>;
  signOut(): Promise<void>;
  /** Returns null when there is no valid session cookie. */
  getSession(): Promise<Session | null>;
  changePassword(data: ChangePasswordData): Promise<void>;
}
