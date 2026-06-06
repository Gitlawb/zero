import { describe, expect, it } from 'bun:test';
import { join } from 'node:path';
import { resolveNpmWrapperTarget, runNpmWrapper } from '../scripts/npm-wrapper';

describe('npm wrapper entrypoint', () => {
  it('points installed zero commands at the wrapper instead of the TS app', async () => {
    const pkg = await Bun.file('package.json').json() as {
      bin?: { zero?: string };
      module?: string;
      scripts?: { dev?: string };
    };

    expect(pkg.bin?.zero).toBe('bin/zero.ts');
    expect(pkg.module).toBe('bin/zero.ts');
    expect(pkg.scripts?.dev).toBe('go run ./cmd/zero');
  });

  it('uses the Go binary and does not fall back to the TS CLI', () => {
    const root = join('repo');
    const existing = new Set([
      join(root, 'zero.exe'),
      join(root, 'src', 'index.ts'),
    ]);

    const native = resolveNpmWrapperTarget({
      root,
      platform: 'win32',
      args: ['--version'],
      exists: (path) => existing.has(path),
    });

    expect(native).toEqual({
      kind: 'native',
      path: join(root, 'zero.exe'),
      command: [join(root, 'zero.exe'), '--version'],
    });

    existing.delete(join(root, 'zero.exe'));
    const fallback = resolveNpmWrapperTarget({
      root,
      platform: 'win32',
      args: ['--version'],
      exists: (path) => existing.has(path),
    });

    expect(fallback).toBeNull();
  });

  it('returns null when the native binary is missing', () => {
    const target = resolveNpmWrapperTarget({
      root: join('repo'),
      platform: 'win32',
      args: ['--version'],
      exists: () => false,
    });

    expect(target).toBeNull();
  });

  it('reports the full no-target state without crashing', async () => {
    let stderr = '';
    const code = await runNpmWrapper({
      root: join('repo'),
      platform: 'linux',
      exists: () => false,
      stderr: { write: (chunk: string) => { stderr += chunk; return true; } },
    });

    expect(code).toBe(1);
    expect(stderr).toContain('No native binary found');
    expect(stderr).toContain('native binary');
    expect(stderr).not.toContain('src/index.ts');
  });

  it('returns a clean exit code when launching the wrapper target throws', async () => {
    let stderr = '';
    const code = await runNpmWrapper({
      root: join('repo'),
      platform: 'linux',
      exists: (path) => path === join('repo', 'zero'),
      spawn: () => { throw new Error('spawn failed'); },
      stderr: { write: (chunk: string) => { stderr += chunk; return true; } },
    });

    expect(code).toBe(1);
    expect(stderr).toContain('Failed to launch wrapper target');
    expect(stderr).toContain('spawn failed');
  });
});
