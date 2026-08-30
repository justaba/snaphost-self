import { SessionAuthProvider } from './session-provider';
import type { AuthProvider } from './provider';

export const auth: AuthProvider = new SessionAuthProvider();
export type { AuthProvider } from './provider';
