export interface User {
  id: string;
  email: string;
  role: 'user' | 'admin';
}

/**
 * A signed-in session.
 *
 * It carries no token. The session is an HttpOnly cookie the browser attaches
 * by itself and this code cannot read — which is the point of it being
 * HttpOnly, and why the previous shape (accessToken, refreshToken, expiresAt)
 * had to go rather than be filled in with blanks. What is left is who the
 * server says you are.
 */
export interface Session {
  user: User;
}

export interface SignInData {
  email: string;
  password: string;
}

export interface ChangePasswordData {
  currentPassword: string;
  newPassword: string;
}

export type AuthErrorCode =
  | 'invalid_credentials'
  | 'weak_password'
  | 'rate_limited'
  | 'network_error'
  | 'unknown';

export interface AuthError {
  code: AuthErrorCode;
  message: string;
}

export type AuthStateListener = (session: Session | null) => void;
export type Unsubscribe = () => void;
