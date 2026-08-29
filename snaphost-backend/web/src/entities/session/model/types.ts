export interface User {
  id: string;
  email: string;
  displayName: string | null;
  avatarUrl: string | null;
  role: 'user' | 'pro' | 'admin';
}

export interface Session {
  accessToken: string;
  refreshToken: string;
  expiresAt: number;
  user: User;
}

export interface SignUpData {
  email: string;
  password: string;
  displayName?: string;
  githubUsername?: string;
  offerAccepted: boolean;
  acceptableUseAccepted: boolean;
  personalDataConsent: boolean;
  ageConfirmed: boolean;
  legalVersion: string;
  acceptedAt: string;
}

export interface SignInData {
  email: string;
  password: string;
}

export type OAuthProvider = 'github' | 'google';

export type AuthErrorCode =
  | 'invalid_credentials'
  | 'email_not_confirmed'
  | 'user_already_exists'
  | 'weak_password'
  | 'rate_limited'
  | 'network_error'
  | 'unknown';

export type AuthError =
  | { code: 'invalid_credentials'; message: string }
  | { code: 'email_not_confirmed'; message: string }
  | { code: 'user_already_exists'; message: string }
  | { code: 'weak_password'; message: string }
  | { code: 'rate_limited'; message: string }
  | { code: 'network_error'; message: string }
  | { code: 'unknown'; message: string };

export type AuthStateListener = (session: Session | null) => void;
export type Unsubscribe = () => void;
