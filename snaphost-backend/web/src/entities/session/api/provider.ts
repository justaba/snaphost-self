import type {
  Session,
  SignUpData,
  SignInData,
  OAuthProvider,
  AuthStateListener,
  Unsubscribe,
} from '../model/types';

export interface AuthProvider {
  signUp(data: SignUpData): Promise<Session | null>;
  signIn(data: SignInData): Promise<Session>;
  signInWithOAuth(provider: OAuthProvider, redirectTo: string): Promise<void>;
  signOut(): Promise<void>;
  getSession(): Promise<Session | null>;
  refreshSession(): Promise<Session | null>;
  onAuthStateChange(listener: AuthStateListener): Unsubscribe;
  sendPasswordResetEmail(email: string, redirectTo: string): Promise<void>;
  updatePassword(newPassword: string): Promise<void>;
}
