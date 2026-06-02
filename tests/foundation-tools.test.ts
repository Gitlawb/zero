import { afterEach, beforeEach, describe, expect, it } from 'bun:test';
import { mkdtemp, readFile, rm, writeFile } from 'fs/promises';
import { tmpdir } from 'os';
import { join } from 'path';
import { applyPatchTool } from '../src/tools/apply_patch';
import { globTool } from '../src/tools/glob';
import { toolRegistry } from '../src/tools';

let dir: string;

beforeEach(async () => {
  dir = await mkdtemp(join(process.cwd(), '.zero-foundation-'));
});

afterEach(async () => {
  await rm(dir, { recursive: true, force: true });
});

describe('package scripts', () => {
  it('exposes the v0.1 foundation validation commands', async () => {
    const pkg = await Bun.file(join(process.cwd(), 'package.json')).json();

    expect(pkg.scripts.typecheck).toBe('bunx tsc --noEmit');
    expect(pkg.scripts.test).toBe('bun test ./tests --timeout 15000');
    expect(pkg.scripts.build).toBe('bun build src/index.ts --compile --outfile zero');
  });
});

describe('tool safety metadata', () => {
  it('marks core tools with side effects and default permission decisions', () => {
    expect(toolRegistry.get('bash')?.safety).toEqual({
      sideEffect: 'shell',
      permission: 'prompt',
      reason: expect.any(String),
    });
    expect(toolRegistry.get('grep')?.safety.sideEffect).toBe('read');
    expect(toolRegistry.get('glob')?.safety.permission).toBe('allow');
    expect(toolRegistry.get('apply_patch')?.safety.sideEffect).toBe('write');
    expect(toolRegistry.get('apply_patch')?.safety.permission).toBe('prompt');
  });
});

describe('globTool', () => {
  it('returns matching paths from the requested cwd', async () => {
    await writeFile(join(dir, 'one.ts'), 'export const one = 1;', 'utf-8');
    await writeFile(join(dir, 'two.txt'), 'two', 'utf-8');

    const result = await globTool.execute({ pattern: '*.ts', cwd: dir });
    expect(result).toContain('one.ts');
    expect(result).not.toContain('two.txt');
  });
});

describe('applyPatchTool', () => {
  it('applies a unified diff patch', async () => {
    const file = join(dir, 'hello.txt');
    await writeFile(file, 'hello\nold\n', 'utf-8');

    const patch = [
      'diff --git a/hello.txt b/hello.txt',
      '--- a/hello.txt',
      '+++ b/hello.txt',
      '@@ -1,2 +1,2 @@',
      ' hello',
      '-old',
      '+new',
      '',
    ].join('\n');

    const result = await applyPatchTool.execute({ patch, cwd: dir });
    expect(result).toBe('Patch applied successfully.');
    const content = await readFile(file, 'utf-8');
    expect(content.replace(/\r\n/g, '\n')).toBe('hello\nnew\n');
  });

  it('refuses to apply patches outside the workspace', async () => {
    const outsideDir = await mkdtemp(join(tmpdir(), 'zero-outside-'));
    try {
      const result = await applyPatchTool.execute({
        cwd: outsideDir,
        patch: 'diff --git a/nope.txt b/nope.txt\n',
      });

      expect(result).toContain('cwd must stay inside the workspace');
    } finally {
      await rm(outsideDir, { recursive: true, force: true });
    }
  });
});
