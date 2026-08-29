import { SupabaseAuthProvider } from './supabase-provider';
import type { AuthProvider } from './provider';

export const auth: AuthProvider = new SupabaseAuthProvider();
export type { AuthProvider } from './provider';
