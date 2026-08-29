import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { basename, dirname, extname, join, relative } from 'node:path';
import { describe, expect, it } from 'vitest';

const root = process.cwd();
const sourceRoot = join(root, 'src');

function filesUnder(directory: string): string[] {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    return entry.isDirectory() ? filesUnder(path) : [path];
  });
}

describe('FSD structure', () => {
  it('keeps every React component next to its own CSS module', () => {
    const components = filesUnder(sourceRoot).filter(
      (file) => extname(file) === '.tsx' && !file.endsWith(join('app', 'entrypoint', 'main.tsx')),
    );

    const missing = components.flatMap((component) => {
      const modulePath = join(dirname(component), `${basename(component, '.tsx')}.module.css`);
      const source = readFileSync(component, 'utf8');
      return existsSync(modulePath) && source.includes(`./${basename(modulePath)}`)
        ? []
        : [relative(root, component)];
    });

    expect(missing).toEqual([]);
  });

  it.each(['pages', 'widgets', 'features', 'entities'])('%s slices expose index.ts', (layer) => {
    const slices = readdirSync(join(sourceRoot, layer), { withFileTypes: true }).filter((entry) =>
      entry.isDirectory(),
    );
    const withoutPublicApi = slices
      .filter((slice) => !existsSync(join(sourceRoot, layer, slice.name, 'index.ts')))
      .map((slice) => slice.name);

    expect(withoutPublicApi).toEqual([]);
  });

  it('does not recreate legacy technical buckets or source-local tests', () => {
    const forbidden = ['components', 'hooks', 'layouts', 'test', 'types']
      .map((name) => join(sourceRoot, name))
      .filter(existsSync)
      .map((path) => relative(root, path));
    const sourceTests = filesUnder(sourceRoot)
      .filter((file) => /\.(test|spec)\.[cm]?[jt]sx?$/.test(file))
      .map((file) => relative(root, file));

    expect([...forbidden, ...sourceTests]).toEqual([]);
  });
});
