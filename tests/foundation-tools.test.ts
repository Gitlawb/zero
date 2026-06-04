import { afterEach, beforeEach, describe, expect, it } from 'bun:test';
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'fs/promises';
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
    expect(pkg.scripts['test:go']).toBe('go test ./...');
    expect(pkg.scripts.build).toBe('bun run scripts/build.ts');
    expect(pkg.scripts['build:go']).toBe('go build ./cmd/zero');
    expect(pkg.scripts['smoke:build']).toBe('bun run scripts/smoke-build.ts');
    expect(pkg.scripts['smoke:go']).toBe('bun run scripts/smoke-go.ts');
  });

  it('keeps CI wired to the stable project commands', async () => {
    const workflow = await readFile(join(process.cwd(), '.github/workflows/ci.yml'), 'utf-8');

    expect(workflow).toContain('run: bun run test:go');
    expect(workflow).toContain('run: bun run build:go');
    expect(workflow).toContain('run: bun run smoke:go');
    expect(workflow).toContain('run: bun run test');
    expect(workflow).toContain('run: bun run typecheck');
    expect(workflow).toContain('run: bun run smoke:build');
  });

  it('documents the npm wrapper smoke checklist', async () => {
    const checklist = await readFile(join(process.cwd(), 'docs/NPM_WRAPPER_SMOKE.md'), 'utf-8');

    expect(checklist).toContain('bun install --frozen-lockfile');
    expect(checklist).toContain('bun run smoke:build');
    expect(checklist).toContain('node_modules/.bin');
    expect(checklist).toContain('bun run smoke:go');
  });
});

describe('tool safety metadata', () => {
  it('marks every registered tool with valid safety metadata', () => {
    const sideEffects = ['read', 'write', 'shell', 'network', 'out_of_workspace'];
    const permissions = ['allow', 'prompt', 'deny'];

    for (const tool of toolRegistry.getAll()) {
      expect(sideEffects).toContain(tool.safety.sideEffect);
      expect(permissions).toContain(tool.safety.permission);
      expect(tool.safety.reason.length).toBeGreaterThan(0);
    }
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

  it('respects limit without collecting an extra match', async () => {
    await writeFile(join(dir, 'a.ts'), '', 'utf-8');
    await writeFile(join(dir, 'b.ts'), '', 'utf-8');
    await writeFile(join(dir, 'c.ts'), '', 'utf-8');

    const result = await globTool.execute({ pattern: '*.ts', cwd: dir, limit: 2 });
    const lines = result.split('\n');
    expect(lines.filter(line => line.endsWith('.ts'))).toHaveLength(2);
    expect(result).toContain('truncated after 2 matches');
  });

  it('can include directory matches', async () => {
    await mkdir(join(dir, 'src'), { recursive: true });
    await writeFile(join(dir, 'src', 'index.ts'), 'export {};', 'utf-8');

    const result = await globTool.execute({ pattern: 'src', cwd: dir, include_dirs: true });
    expect(result).toContain('src');
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
