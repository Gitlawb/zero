import { afterEach, describe, expect, it } from 'bun:test';
import { mkdir, mkdtemp, rm, symlink, writeFile } from 'fs/promises';
import { tmpdir } from 'os';
import { join } from 'path';
import {
  loadZeroPlugins,
  parseZeroPluginManifest,
  resolveZeroPluginRoots,
} from '../src/zero-plugins';

const tempDirs: string[] = [];

afterEach(async () => {
  await Promise.all(tempDirs.splice(0).map((dir) => rm(dir, { recursive: true, force: true })));
});

async function makeTempDir(): Promise<string> {
  const dir = await mkdtemp(join(tmpdir(), 'zero-plugins-'));
  tempDirs.push(dir);
  return dir;
}

async function writePluginManifest(
  pluginDir: string,
  manifest: Record<string, unknown>
): Promise<void> {
  await mkdir(pluginDir, { recursive: true });
  await writeFile(join(pluginDir, 'plugin.json'), JSON.stringify(manifest, null, 2), 'utf-8');
}

async function runZeroPlugins(
  cwd: string,
  args: string[] = []
): Promise<{ exitCode: number; stdout: string; stderr: string }> {
  const child = Bun.spawn([process.execPath, join(process.cwd(), 'src/index.ts'), 'plugins', ...args], {
    cwd,
    env: {
      ...process.env,
      HOME: join(cwd, 'home'),
      USERPROFILE: join(cwd, 'home'),
      XDG_CONFIG_HOME: join(cwd, 'xdg'),
    },
    stderr: 'pipe',
    stdout: 'pipe',
  });

  const [exitCode, stdout, stderr] = await Promise.all([
    child.exited,
    new Response(child.stdout).text(),
    new Response(child.stderr).text(),
  ]);

  return { exitCode, stdout, stderr };
}

describe('Zero plugin manifest validation', () => {
  it('normalizes extension metadata and resolves plugin-local paths', async () => {
    const dir = await makeTempDir();
    const pluginDir = join(dir, 'plugins', 'zero-demo');

    const parsed = parseZeroPluginManifest({
      schemaVersion: 1,
      id: 'zero.demo',
      name: 'Zero Demo',
      version: '0.1.0',
      description: 'Demo plugin',
      tools: [{
        name: 'lookup',
        description: 'Lookup docs',
        command: 'node',
        args: ['tools/lookup.mjs'],
        inputSchema: { type: 'object', properties: { query: { type: 'string' } } },
        permission: 'prompt',
      }],
      prompts: [{ name: 'review', path: 'prompts/review.md' }],
      skills: [{ name: 'ts-review', path: 'skills/ts-review/SKILL.md' }],
      hooks: [{ name: 'pre-tool', event: 'beforeTool', command: 'node', args: ['hooks/pre-tool.mjs'] }],
    }, {
      manifestPath: join(pluginDir, 'plugin.json'),
      pluginDir,
      root: join(dir, 'plugins'),
      source: 'project',
    });

    expect(parsed).toMatchObject({
      id: 'zero.demo',
      name: 'Zero Demo',
      version: '0.1.0',
      enabled: true,
      source: 'project',
    });
    expect(parsed.tools).toEqual([
      expect.objectContaining({
        name: 'lookup',
        permission: 'prompt',
        args: ['tools/lookup.mjs'],
      }),
    ]);
    expect(parsed.prompts[0]?.path).toBe(join(pluginDir, 'prompts', 'review.md'));
    expect(parsed.skills[0]?.path).toBe(join(pluginDir, 'skills', 'ts-review', 'SKILL.md'));
    expect(parsed.hooks[0]).toEqual(expect.objectContaining({
      name: 'pre-tool',
      event: 'beforeTool',
      command: 'node',
      args: ['hooks/pre-tool.mjs'],
    }));
  });

  it('clamps manifest tool auto-approval unless explicitly enabled', async () => {
    const dir = await makeTempDir();
    const pluginDir = join(dir, 'plugins', 'zero-demo');
    const manifest = {
      schemaVersion: 1,
      id: 'zero.demo',
      name: 'Zero Demo',
      version: '0.1.0',
      tools: [{
        name: 'lookup',
        command: 'node',
        permission: 'allow',
      }],
    };
    const options = {
      manifestPath: join(pluginDir, 'plugin.json'),
      pluginDir,
      root: join(dir, 'plugins'),
      source: 'project' as const,
    };

    expect(parseZeroPluginManifest(manifest, options).tools[0]?.permission).toBe('prompt');
    expect(parseZeroPluginManifest(manifest, {
      ...options,
      allowManifestToolAutoApproval: true,
    }).tools[0]?.permission).toBe('allow');
  });

  it('rejects unsafe plugin-local paths', async () => {
    const dir = await makeTempDir();
    const pluginDir = join(dir, 'plugins', 'bad');
    const options = {
      manifestPath: join(pluginDir, 'plugin.json'),
      pluginDir,
      root: join(dir, 'plugins'),
      source: 'project' as const,
    };

    for (const path of ['../outside.md', '/tmp/escape.md', 'C:\\Windows\\escape.md']) {
      expect(() => parseZeroPluginManifest({
        schemaVersion: 1,
        id: 'zero.bad',
        name: 'Bad',
        version: '0.1.0',
        prompts: [{ name: 'escape', path }],
      }, options)).toThrow('must stay inside the plugin directory');
    }
  });

  it('rejects symlink-parent escapes with a missing leaf', async () => {
    const dir = await makeTempDir();
    const pluginDir = join(dir, 'plugins', 'bad');
    const outside = join(dir, 'outside');
    await mkdir(pluginDir, { recursive: true });
    await mkdir(outside, { recursive: true });
    await symlink(outside, join(pluginDir, 'link'), process.platform === 'win32' ? 'junction' : 'dir');

    expect(() => parseZeroPluginManifest({
      schemaVersion: 1,
      id: 'zero.bad',
      name: 'Bad',
      version: '0.1.0',
      prompts: [{ name: 'escape', path: join('link', 'missing.md') }],
    }, {
      manifestPath: join(pluginDir, 'plugin.json'),
      pluginDir,
      root: join(dir, 'plugins'),
      source: 'project',
    })).toThrow('must stay inside the plugin directory');
  });

});

describe('Zero local plugin loader', () => {
  it('discovers user and project plugin manifests with project precedence', async () => {
    const dir = await makeTempDir();
    const userRoot = join(dir, 'user-plugins');
    const projectRoot = join(dir, 'project-plugins');
    await writePluginManifest(join(userRoot, 'demo'), {
      schemaVersion: 1,
      id: 'zero.demo',
      name: 'User Demo',
      version: '0.1.0',
    });
    await writePluginManifest(join(projectRoot, 'demo'), {
      schemaVersion: 1,
      id: 'zero.demo',
      name: 'Project Demo',
      version: '0.2.0',
      enabled: false,
    });
    await writePluginManifest(join(projectRoot, 'docs'), {
      schemaVersion: 1,
      id: 'zero.docs',
      name: 'Docs',
      version: '1.0.0',
      skills: [{ name: 'docs', path: 'skills/docs/SKILL.md' }],
    });

    const result = await loadZeroPlugins({
      roots: [
        { source: 'user', path: userRoot },
        { source: 'project', path: projectRoot },
      ],
    });

    expect(result.plugins.map((plugin) => `${plugin.id}:${plugin.name}:${plugin.version}`)).toEqual([
      'zero.demo:Project Demo:0.2.0',
      'zero.docs:Docs:1.0.0',
    ]);
    expect(result.plugins[0]?.enabled).toBe(false);
    expect(result.diagnostics).toEqual([
      expect.objectContaining({
        kind: 'duplicate',
        pluginId: 'zero.demo',
      }),
    ]);
  });

  it('keeps loading valid plugins when one manifest is invalid', async () => {
    const dir = await makeTempDir();
    const root = join(dir, 'plugins');
    await writePluginManifest(join(root, 'good'), {
      schemaVersion: 1,
      id: 'zero.good',
      name: 'Good',
      version: '1.0.0',
    });
    await writePluginManifest(join(root, 'bad'), {
      schemaVersion: 2,
      id: 'zero.bad',
      name: 'Bad',
      version: '1.0.0',
    });

    const result = await loadZeroPlugins({
      roots: [{ source: 'project', path: root }],
    });

    expect(result.plugins.map((plugin) => plugin.id)).toEqual(['zero.good']);
    expect(result.diagnostics).toEqual([
      expect.objectContaining({
        kind: 'schema',
        pluginPath: join(root, 'bad'),
      }),
    ]);
  });

  it('keeps loading valid plugins when one manifest has malformed JSON', async () => {
    const dir = await makeTempDir();
    const root = join(dir, 'plugins');
    await writePluginManifest(join(root, 'good'), {
      schemaVersion: 1,
      id: 'zero.good',
      name: 'Good',
      version: '1.0.0',
    });
    await mkdir(join(root, 'bad'), { recursive: true });
    await writeFile(join(root, 'bad', 'plugin.json'), '{ invalid json }', 'utf-8');

    const result = await loadZeroPlugins({
      roots: [{ source: 'project', path: root }],
    });

    expect(result.plugins.map((plugin) => plugin.id)).toEqual(['zero.good']);
    expect(result.diagnostics).toEqual([
      expect.objectContaining({
        kind: 'json',
        pluginPath: join(root, 'bad'),
      }),
    ]);
  });

  it('resolves default user and project plugin roots', async () => {
    const dir = await makeTempDir();

    expect(resolveZeroPluginRoots({
      cwd: dir,
      env: {
        XDG_CONFIG_HOME: join(dir, 'xdg'),
      },
    })).toEqual([
      { source: 'user', path: join(dir, 'xdg', 'zero', 'plugins') },
      { source: 'project', path: join(dir, '.zero', 'plugins') },
    ]);
  });
});

describe('zero plugins CLI', () => {
  it('lists local plugin manifests as JSON', async () => {
    const dir = await makeTempDir();
    await writePluginManifest(join(dir, '.zero', 'plugins', 'docs'), {
      schemaVersion: 1,
      id: 'zero.docs',
      name: 'Docs',
      version: '1.0.0',
      prompts: [{ name: 'review', path: 'prompts/review.md' }],
    });

    const result = await runZeroPlugins(dir, ['list', '--json']);

    expect(result.exitCode).toBe(0);
    expect(result.stderr.trim()).toBe('');
    expect(JSON.parse(result.stdout)).toEqual({
      plugins: [
        expect.objectContaining({
          id: 'zero.docs',
          name: 'Docs',
          version: '1.0.0',
          source: 'project',
        }),
      ],
      diagnostics: [],
    });
  });

  it('lists local plugin manifests as formatted text', async () => {
    const dir = await makeTempDir();
    await writePluginManifest(join(dir, '.zero', 'plugins', 'docs'), {
      schemaVersion: 1,
      id: 'zero.docs',
      name: 'Docs',
      version: '1.0.0',
      prompts: [{ name: 'review', path: 'prompts/review.md' }],
    });

    const result = await runZeroPlugins(dir, ['list']);

    expect(result.exitCode).toBe(0);
    expect(result.stderr.trim()).toBe('');
    expect(result.stdout).toContain('zero.docs');
    expect(result.stdout).toContain('Docs');
    expect(result.stdout).toContain('1.0.0');
    expect(result.stdout).toMatch(/1\s+prompts?/);
  });
});
