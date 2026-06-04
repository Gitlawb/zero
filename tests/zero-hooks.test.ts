import { afterEach, describe, expect, it } from 'bun:test';
import { mkdir, mkdtemp, rm, writeFile } from 'fs/promises';
import { tmpdir } from 'os';
import { dirname, join } from 'path';
import {
  ZeroHookAuditStore,
  ZeroHookConfigStore,
  formatZeroHookList,
  loadZeroHooksConfig,
  resolveZeroHookPaths,
  selectZeroHooks,
} from '../src/zero-hooks';

const tempDirs: string[] = [];

afterEach(async () => {
  await Promise.all(tempDirs.splice(0).map((dir) => rm(dir, { recursive: true, force: true })));
});

async function makeTempDir(): Promise<string> {
  const dir = await mkdtemp(join(tmpdir(), 'zero-hooks-'));
  tempDirs.push(dir);
  return dir;
}

async function writeJson(path: string, value: unknown): Promise<void> {
  await mkdir(dirname(path), { recursive: true });
  await writeFile(path, JSON.stringify(value, null, 2), 'utf-8');
}

async function runZeroHooks(
  cwd: string,
  args: string[] = []
): Promise<{ exitCode: number; stdout: string; stderr: string }> {
  const cliEntrypoint = join(import.meta.dir, '..', 'src', 'index.ts');
  const child = Bun.spawn([process.execPath, cliEntrypoint, 'hooks', ...args], {
    cwd,
    env: {
      ...process.env,
      HOME: join(cwd, 'home'),
      USERPROFILE: join(cwd, 'home'),
      XDG_CONFIG_HOME: join(cwd, 'xdg-config'),
      XDG_DATA_HOME: join(cwd, 'xdg-data'),
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

describe('Zero hook config backend', () => {
  it('resolves default hook config and audit paths', async () => {
    const dir = await makeTempDir();

    expect(resolveZeroHookPaths({
      cwd: dir,
      env: {
        XDG_CONFIG_HOME: join(dir, 'config'),
        XDG_DATA_HOME: join(dir, 'data'),
      },
    })).toEqual({
      userConfigPath: join(dir, 'config', 'zero', 'hooks.json'),
      projectConfigPath: join(dir, '.zero', 'hooks.json'),
      auditPath: join(dir, 'data', 'zero', 'hooks', 'audit.jsonl'),
    });
  });

  it('loads layered hook config with project overrides and diagnostics', async () => {
    const dir = await makeTempDir();
    const userConfigPath = join(dir, 'user-hooks.json');
    const projectConfigPath = join(dir, 'project-hooks.json');
    await writeJson(userConfigPath, {
      enabled: true,
      hooks: [
        {
          id: 'zero.format',
          name: 'Format after edits',
          event: 'afterTool',
          matcher: 'edit_file',
          command: 'bun',
          args: ['run', 'format'],
        },
        {
          id: 'zero.audit',
          event: 'sessionEnd',
          command: 'node',
          args: ['audit.mjs'],
        },
      ],
    });
    await writeJson(projectConfigPath, {
      hooks: [
        {
          id: 'zero.format',
          event: 'afterTool',
          matcher: 'write_file',
          command: 'bun',
          args: ['run', 'lint'],
          enabled: false,
        },
      ],
    });

    const result = await loadZeroHooksConfig({ userConfigPath, projectConfigPath });

    expect(result.config.enabled).toBe(true);
    expect(result.config.hooks).toEqual([
      expect.objectContaining({
        id: 'zero.audit',
        enabled: true,
        event: 'sessionEnd',
      }),
      expect.objectContaining({
        id: 'zero.format',
        enabled: false,
        event: 'afterTool',
        matcher: 'write_file',
        args: ['run', 'lint'],
      }),
    ]);
    expect(result.diagnostics).toEqual([
      expect.objectContaining({
        kind: 'duplicate',
        hookId: 'zero.format',
      }),
    ]);
  });

  it('rejects matchers on session hooks with a schema diagnostic', async () => {
    const dir = await makeTempDir();
    const projectConfigPath = join(dir, 'hooks.json');
    await writeJson(projectConfigPath, {
      hooks: [{
        id: 'zero.session',
        event: 'sessionStart',
        matcher: 'bash',
        command: 'node',
      }],
    });

    const result = await loadZeroHooksConfig({
      userConfigPath: join(dir, 'missing-user-hooks.json'),
      projectConfigPath,
    });

    expect(result.config.hooks).toEqual([]);
    expect(result.diagnostics).toEqual([
      expect.objectContaining({
        kind: 'schema',
        fieldPath: 'hooks.0.matcher',
      }),
    ]);
  });

  it('persists hook updates through the config store', async () => {
    const dir = await makeTempDir();
    const configPath = join(dir, 'hooks.json');
    const store = new ZeroHookConfigStore({ configPath });

    await store.upsert({
      id: 'zero.preflight',
      name: 'Preflight',
      event: 'beforeTool',
      matcher: 'bash',
      command: 'node',
      args: ['hooks/preflight.mjs'],
    });
    await store.setEnabled('zero.preflight', false);

    const result = await loadZeroHooksConfig({ projectConfigPath: configPath });
    expect(result.config.hooks).toEqual([
      expect.objectContaining({
        id: 'zero.preflight',
        enabled: false,
        matcher: 'bash',
      }),
    ]);

    expect(await store.remove('zero.preflight')).toBe(true);
    expect((await store.list()).hooks).toEqual([]);
  });

  it('serializes concurrent config store mutations for the same path', async () => {
    const dir = await makeTempDir();
    const configPath = join(dir, 'hooks.json');
    const first = new ZeroHookConfigStore({ configPath });
    const second = new ZeroHookConfigStore({ configPath });

    await Promise.all([
      first.upsert({
        id: 'zero.first',
        event: 'beforeTool',
        matcher: 'read_*',
        command: 'node',
        args: [],
      }),
      second.upsert({
        id: 'zero.second',
        event: 'afterTool',
        matcher: 'write_*',
        command: 'node',
        args: [],
      }),
    ]);

    expect((await first.list()).hooks.map((hook) => hook.id)).toEqual(['zero.first', 'zero.second']);
  });

  it('selects enabled hooks by event and matcher', () => {
    const config = {
      enabled: true,
      hooks: [
        {
          id: 'zero.reads',
          event: 'beforeTool',
          matcher: 'read_*',
          command: 'node',
          args: [],
          enabled: true,
        },
        {
          id: 'zero.shell',
          event: 'beforeTool',
          matcher: 'bash',
          command: 'node',
          args: [],
          enabled: false,
        },
        {
          id: 'zero.done',
          event: 'sessionEnd',
          command: 'node',
          args: [],
          enabled: true,
        },
        {
          id: 'zero.shell-edit',
          event: 'beforeTool',
          matcher: 'shell_*_edit',
          command: 'node',
          args: [],
          enabled: true,
        },
      ],
    } satisfies Parameters<typeof selectZeroHooks>[0];

    expect(selectZeroHooks(config, {
      event: 'beforeTool',
      toolName: 'read_file',
    }).map((hook) => hook.id)).toEqual(['zero.reads']);

    expect(selectZeroHooks(config, {
      event: 'beforeTool',
      toolName: 'shell_safe_edit',
    }).map((hook) => hook.id)).toEqual(['zero.shell-edit']);

    expect(selectZeroHooks(config, {
      event: 'beforeTool',
      toolName: 'shell_safe_view',
    })).toEqual([]);
  });

  it('formats hook config for CLI and UI consumers', () => {
    expect(formatZeroHookList({
      enabled: true,
      hooks: [{
        id: 'zero.preflight',
        name: 'Preflight',
        event: 'beforeTool',
        matcher: 'bash',
        command: 'node',
        args: ['hooks/preflight.mjs'],
        enabled: true,
      }],
    }, [])).toContain('zero.preflight');
  });
});

describe('Zero hook audit backend', () => {
  it('appends and reads hook audit events as JSONL', async () => {
    const dir = await makeTempDir();
    const auditPath = join(dir, 'audit.jsonl');
    const audit = new ZeroHookAuditStore({
      auditPath,
      now: () => new Date('2026-06-04T00:00:00.000Z'),
    });

    await audit.appendStarted({
      hookId: 'zero.preflight',
      event: 'beforeTool',
      matcher: 'bash',
      commands: [{ command: 'node', args: ['hooks/preflight.mjs'] }],
      toolCallId: 'call_1',
    });
    await audit.appendCompleted({
      hookId: 'zero.preflight',
      event: 'beforeTool',
      matcher: 'bash',
      status: 'completed',
      results: [{ exitCode: 0, stdout: 'ok', stderr: '' }],
      toolCallId: 'call_1',
      durationMs: 12,
    });

    expect(await audit.readEvents()).toEqual([
      expect.objectContaining({
        sequence: 1,
        type: 'hook_execution_started',
        hookId: 'zero.preflight',
        createdAt: '2026-06-04T00:00:00.000Z',
      }),
      expect.objectContaining({
        sequence: 2,
        type: 'hook_execution_completed',
        status: 'completed',
        durationMs: 12,
      }),
    ]);
  });

  it('serializes concurrent appends and skips malformed audit lines', async () => {
    const dir = await makeTempDir();
    const auditPath = join(dir, 'audit.jsonl');
    await writeFile(auditPath, [
      JSON.stringify({
        sequence: 1,
        createdAt: '2026-06-04T00:00:00.000Z',
        type: 'hook_execution_started',
        hookId: 'zero.seed',
        event: 'sessionStart',
        commands: [{ command: 'node', args: [] }],
      }),
      '{not-json',
      JSON.stringify({
        sequence: 2,
        createdAt: '2026-06-04T00:00:01.000Z',
        type: 'hook_execution_completed',
        hookId: 'zero.seed',
        event: 'sessionStart',
        status: 'completed',
      }),
      '',
    ].join('\n'), 'utf-8');
    const audit = new ZeroHookAuditStore({
      auditPath,
      now: () => new Date('2026-06-04T00:00:02.000Z'),
    });

    expect((await audit.readEvents()).map((event) => event.sequence)).toEqual([1, 2]);

    const appended = await Promise.all([
      audit.appendStarted({
        hookId: 'zero.one',
        event: 'sessionStart',
        commands: [{ command: 'node', args: [] }],
      }),
      audit.appendStarted({
        hookId: 'zero.two',
        event: 'sessionStart',
        commands: [{ command: 'node', args: [] }],
      }),
    ]);

    expect(appended.map((event) => event.sequence)).toEqual([3, 4]);
    expect((await audit.readEvents()).map((event) => event.sequence)).toEqual([1, 2, 3, 4]);
  });
});

describe('zero hooks CLI', () => {
  it('lists project hook config as JSON and formatted text', async () => {
    const dir = await makeTempDir();
    const syntheticSecret = `sk-proj-${'a'.repeat(24)}`;
    await writeJson(join(dir, '.zero', 'hooks.json'), {
      hooks: [{
        id: 'zero.preflight',
        event: 'beforeTool',
        matcher: 'bash',
        command: 'node',
        args: ['hooks/preflight.mjs', syntheticSecret],
      }],
    });

    const jsonResult = await runZeroHooks(dir, ['list', '--json']);
    expect(jsonResult.exitCode).toBe(0);
    expect(jsonResult.stderr.trim()).toBe('');
    expect(JSON.parse(jsonResult.stdout)).toEqual({
      hooks: expect.objectContaining({
        enabled: true,
        hooks: [expect.objectContaining({ id: 'zero.preflight', enabled: true })],
      }),
      diagnostics: [],
    });
    expect(jsonResult.stdout).toContain('[REDACTED]');
    expect(jsonResult.stdout).not.toContain(syntheticSecret);

    const textResult = await runZeroHooks(dir, ['list']);
    expect(textResult.exitCode).toBe(0);
    expect(textResult.stderr.trim()).toBe('');
    expect(textResult.stdout).toContain('Zero Hooks');
    expect(textResult.stdout).toContain('zero.preflight');
    expect(textResult.stdout).toContain('beforeTool');
    expect(textResult.stdout).toContain('[REDACTED]');
    expect(textResult.stdout).not.toContain(syntheticSecret);
  });
});
