import {
  selectAuthError,
  selectAuthStatus,
  selectSession,
  selectUser,
  type AuthState,
} from './auth-slice';
import { useSessionSelector } from './store-hooks';
import type { AuthError, Session, User } from './types';

export interface UseAuthResult {
  session: Session | null;
  user: User | null;
  status: AuthState['status'];
  error: AuthError | null;
  isAuthenticated: boolean;
}

export function useAuth(): UseAuthResult {
  const session = useSessionSelector(selectSession);
  const user = useSessionSelector(selectUser);
  const status = useSessionSelector(selectAuthStatus);
  const error = useSessionSelector(selectAuthError);
  const isAuthenticated = status === 'authenticated';
  return { session, user, status, error, isAuthenticated };
}
