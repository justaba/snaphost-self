import { describe, expect, it, vi } from 'vitest';

const { createClient, supabaseClient } = vi.hoisted(() => ({
  createClient: vi.fn(),
  supabaseClient: { auth: {} },
}));

vi.mock('@supabase/supabase-js', () => ({ createClient }));

describe('Supabase browser client', () => {
  it('uses the implicit callback flow with per-tab session storage', async () => {
    createClient.mockReturnValue(supabaseClient);

    const { supabase } = await import('@/shared/api/supabase/client');

    expect(supabase).toBe(supabaseClient);
    expect(createClient).toHaveBeenCalledWith(
      'https://example.supabase.co',
      'sb_publishable_test_only',
      {
        auth: {
          autoRefreshToken: true,
          persistSession: true,
          detectSessionInUrl: true,
          storage: sessionStorage,
          flowType: 'implicit',
        },
      },
    );
  });
});
