import { describe, expect, it } from 'vitest';

import { deployFormSchema, toCreateDeployRequest } from '@/features/create-deploy';

describe('deployFormSchema', () => {
  it('accepts a supported repository and normalizes the request', () => {
    const values = deployFormSchema.parse({
      repo_url: '  https://github.com/acme/app  ',
      branch: ' main ',
      env: [{ key: 'NODE_ENV', value: 'production' }],
    });

    expect(toCreateDeployRequest(values)).toEqual({
      repo_url: 'https://github.com/acme/app',
      branch: 'main',
      env: { NODE_ENV: 'production' },
    });
  });

  it('rejects unsupported repository hosts', () => {
    const result = deployFormSchema.safeParse({
      repo_url: 'https://example.com/acme/app',
      branch: 'main',
      env: [],
    });

    expect(result.success).toBe(false);
  });

  it('rejects reserved and duplicate environment keys', () => {
    const reserved = deployFormSchema.safeParse({
      repo_url: 'https://gitlab.com/acme/app',
      branch: 'main',
      env: [{ key: 'SNAPHOST_TOKEN', value: 'secret' }],
    });
    const duplicate = deployFormSchema.safeParse({
      repo_url: 'https://bitbucket.org/acme/app',
      branch: 'main',
      env: [
        { key: 'API_URL', value: 'one' },
        { key: 'API_URL', value: 'two' },
      ],
    });

    expect(reserved.success).toBe(false);
    expect(duplicate.success).toBe(false);
  });
});
