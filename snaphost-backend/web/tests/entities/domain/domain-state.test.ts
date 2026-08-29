import { describe, expect, it } from 'vitest';

import { describeDomain, hasPublishedTarget, type CustomDomain } from '@/entities/domain';

function domain(overrides: Partial<CustomDomain> = {}): CustomDomain {
  return {
    id: 'domain-1',
    user_id: 'user-1',
    project_id: 'project-1',
    target_deploy_id: null,
    domain: 'example.com',
    verification_token: 'token',
    status: 'pending',
    created_at: '2026-08-15T10:00:00Z',
    updated_at: '2026-08-15T10:00:00Z',
    dns: {
      verification_record: '_snaphost.example.com',
      verification_type: 'TXT',
      verification_value: 'token',
      apex_note: 'Use an A record for the apex.',
    },
    ...overrides,
  };
}

describe('domain presentation', () => {
  it('distinguishes a verified but unpublished domain', () => {
    expect(describeDomain(domain({ status: 'verified' }))).toMatchObject({
      label: 'Не опубликован',
      tone: 'pending',
    });
  });

  it('detects whether the backend published a DNS target', () => {
    expect(hasPublishedTarget(domain())).toBe(false);
    expect(
      hasPublishedTarget(domain({ dns: { ...domain().dns, cname_target: 'edge.snaphost.app' } })),
    ).toBe(true);
  });
});
