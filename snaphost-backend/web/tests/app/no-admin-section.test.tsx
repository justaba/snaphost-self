import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it } from 'vitest';

import { Sidebar } from '@/widgets/dashboard-layout';

const root = process.cwd();

/**
 * The operator console is gone. It was a second, role-gated copy of the
 * dashboard's own screens, and it existed because the platform this forked
 * from had many accounts and one administrator over them — here those are the
 * same person.
 *
 * These assert the absence rather than trusting the deletion, because the
 * panel has already shipped one navigation entry that outlived its page:
 * Транзакции pointed at a route deleted with billing and nothing noticed.
 */
describe('the admin section is gone', () => {
  it('leaves no admin pages, widgets or entity behind', () => {
    const orphans = [
      'src/pages/admin-overview',
      'src/pages/admin-users',
      'src/pages/admin-user',
      'src/pages/admin-projects',
      'src/pages/admin-deploys',
      'src/pages/admin-deploy',
      'src/pages/admin-domains',
      'src/widgets/admin-layout',
      'src/widgets/admin-deploy-list',
      'src/entities/admin',
    ].filter((path) => existsSync(join(root, path)));

    expect(orphans).toEqual([]);
  });

  /** A route table entry outliving its page is the exact failure mode. */
  it('declares no admin routes beyond the redirect', () => {
    const app = readFileSync(join(root, 'src/app/App.tsx'), 'utf8');

    expect(app).not.toMatch(/AdminLayout|AdminOverviewPage|AdminDeploysPage|AdminDomainsPage/);
    expect(app).toMatch(/admin\/\*/);
  });

  it('offers no navigation into it', () => {
    render(
      <MemoryRouter>
        <Sidebar />
      </MemoryRouter>,
    );

    expect(screen.queryByRole('link', { name: /Администрирование/ })).toBeNull();
    expect(screen.queryByRole('link', { name: /Транзакции/ })).toBeNull();
    expect(screen.getByRole('link', { name: /Проекты/ })).toHaveAttribute(
      'href',
      '/dashboard/projects',
    );
  });

  /**
   * Every /api/v1/admin path is refused by Casbin now, so a call to one is a
   * guaranteed 403 rather than a screen that half works.
   */
  it('calls no admin endpoint from anywhere in the panel', () => {
    const offenders: string[] = [];
    const walk = (dir: string) => {
      for (const entry of readdirSync(dir, { withFileTypes: true })) {
        const path = join(dir, entry.name);
        if (entry.isDirectory()) walk(path);
        else if (/\.tsx?$/.test(entry.name) && readFileSync(path, 'utf8').includes('/api/v1/admin'))
          offenders.push(path.slice(root.length + 1));
      }
    };
    walk(join(root, 'src'));

    expect(offenders).toEqual([]);
  });
});
