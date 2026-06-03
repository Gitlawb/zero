import { afterEach, describe, expect, it } from 'bun:test';
import { mkdir, mkdtemp, rm } from 'fs/promises';
import { tmpdir } from 'os';
import { join } from 'path';
import {
  ZeroMcpPermissionStore,
  type ZeroMcpPermissionAutonomy,
} from '../src/zero-mcp';

const tempDirs: string[] = [];

afterEach(async () => {
  await Promise.all(tempDirs.splice(0).map((dir) => rm(dir, { recursive: true, force: true })));
});

async function makeTempDir(): Promise<string> {
  const dir = await mkdtemp(join(tmpdir(), 'zero-mcp-permissions-'));
  tempDirs.push(dir);
  return dir;
}

async function makeStore(): Promise<{
  dir: string;
  permissionPath: string;
  store: ZeroMcpPermissionStore;
}> {
  const dir = await makeTempDir();
  const permissionPath = join(dir, 'mcp-permissions.json');
  const store = new ZeroMcpPermissionStore({
    filePath: permissionPath,
    now: () => new Date('2026-06-03T09:30:00.000Z'),
  });
  return { dir, permissionPath, store };
}

async function runZeroMcpPermissions(
  cwd: string,
  args: string[],
  permissionPath: string
): Promise<{ exitCode: number; stdout: string; stderr: string }> {
  const child = Bun.spawn(
    [process.execPath, join(process.cwd(), 'src/index.ts'), 'mcp', 'permissions', ...args],
    {
      cwd,
      env: {
        ...process.env,
        HOME: join(cwd, 'home'),
        USERPROFILE: join(cwd, 'home'),
        ZERO_MCP_PERMISSIONS_PATH: permissionPath,
      },
      stderr: 'pipe',
      stdout: 'pipe',
    }
  );

  const [exitCode, stdout, stderr] = await Promise.all([
    child.exited,
    new Response(child.stdout).text(),
    new Response(child.stderr).text(),
  ]);

  return { exitCode, stdout, stderr };
}

describe('Zero MCP permission store', () => {
  it('persists identity-aware tool grants and enforces the autonomy ceiling', async () => {
    const { permissionPath, store } = await makeStore();

    await store.grantTool({
      serverName: 'docs',
      serverIdentity: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
      toolName: 'lookup',
      maxAutonomy: 'medium',
    });

    expect(await Bun.file(permissionPath).exists()).toBe(true);
    expect(await store.isToolPersistentlyApproved({
      serverName: 'docs',
      serverIdentity: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
      toolName: 'lookup',
      requestedAutonomy: 'medium',
    })).toBe(true);
    expect(await store.isToolPersistentlyApproved({
      serverName: 'docs',
      serverIdentity: 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
      toolName: 'lookup',
      requestedAutonomy: 'medium',
    })).toBe(false);
    expect(await store.isToolPersistentlyApproved({
      serverName: 'docs',
      serverIdentity: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
      toolName: 'lookup',
      requestedAutonomy: 'high',
    })).toBe(false);
  });

  it('allows server grants to cover tools and server revocation to cascade', async () => {
    const { store } = await makeStore();

    await store.grantServer({
      serverName: 'docs',
      serverIdentity: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
      maxAutonomy: 'high',
    });
    await store.grantTool({
      serverName: 'docs',
      serverIdentity: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
      toolName: 'lookup',
      maxAutonomy: 'low',
    });
    await store.grantTool({
      serverName: 'other',
      serverIdentity: 'cccccccccccccccccccccccccccccccc',
      toolName: 'search',
      maxAutonomy: 'low',
    });

    expect(await store.isToolPersistentlyApproved({
      serverName: 'docs',
      serverIdentity: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
      toolName: 'write',
      requestedAutonomy: 'high',
    })).toBe(true);

    const revoked = await store.revokeServer('docs');

    expect(revoked).toBe(2);
    expect(await store.list()).toEqual([
      expect.objectContaining({
        scope: 'tool',
        serverName: 'other',
        toolName: 'search',
      }),
    ]);
  });

  it('lists grants in stable server/tool order and clears them', async () => {
    const { store } = await makeStore();

    await store.grantTool({
      serverName: 'zeta',
      serverIdentity: 'zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz',
      toolName: 'lookup',
      maxAutonomy: 'low',
    });
    await store.grantServer({
      serverName: 'alpha',
      serverIdentity: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
      maxAutonomy: 'medium',
    });

    expect(await store.list()).toEqual([
      expect.objectContaining({ scope: 'server', serverName: 'alpha' }),
      expect.objectContaining({ scope: 'tool', serverName: 'zeta', toolName: 'lookup' }),
    ]);

    expect(await store.clear()).toBe(2);
    expect(await store.list()).toEqual([]);
  });

  it('rejects invalid autonomy values before writing a grant', async () => {
    const { store } = await makeStore();

    await expect(store.grantServer({
      serverName: 'docs',
      serverIdentity: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
      maxAutonomy: 'urgent' as ZeroMcpPermissionAutonomy,
    })).rejects.toThrow('Invalid MCP permission autonomy');

    expect(await store.list()).toEqual([]);
  });
});

describe('zero mcp permissions CLI', () => {
  it('lists and revokes MCP grants as JSON', async () => {
    const { dir, permissionPath, store } = await makeStore();
    await mkdir(join(dir, '.zero'), { recursive: true });
    await store.grantServer({
      serverName: 'docs',
      serverIdentity: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
      maxAutonomy: 'medium',
    });
    await store.grantTool({
      serverName: 'docs',
      serverIdentity: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
      toolName: 'lookup',
      maxAutonomy: 'low',
    });

    const listResult = await runZeroMcpPermissions(dir, ['list', '--json'], permissionPath);

    expect(listResult.exitCode).toBe(0);
    expect(listResult.stderr.trim()).toBe('');
    expect(JSON.parse(listResult.stdout).permissions).toEqual([
      expect.objectContaining({
        scope: 'server',
        serverName: 'docs',
        maxAutonomy: 'medium',
      }),
      expect.objectContaining({
        scope: 'tool',
        serverName: 'docs',
        toolName: 'lookup',
        maxAutonomy: 'low',
      }),
    ]);

    const revokeToolResult = await runZeroMcpPermissions(
      dir,
      ['revoke', 'docs', 'lookup', '--json'],
      permissionPath
    );

    expect(revokeToolResult.exitCode).toBe(0);
    expect(revokeToolResult.stderr.trim()).toBe('');
    expect(JSON.parse(revokeToolResult.stdout)).toEqual({
      revoked: 1,
      scope: 'tool',
      serverName: 'docs',
      toolName: 'lookup',
    });

    const revokeServerResult = await runZeroMcpPermissions(
      dir,
      ['revoke', 'docs', '--json'],
      permissionPath
    );

    expect(revokeServerResult.exitCode).toBe(0);
    expect(revokeServerResult.stderr.trim()).toBe('');
    expect(JSON.parse(revokeServerResult.stdout)).toEqual({
      revoked: 1,
      scope: 'server',
      serverName: 'docs',
    });
    expect(await store.list()).toEqual([]);
  });

  it('requires confirmation before clearing all MCP grants', async () => {
    const { dir, permissionPath, store } = await makeStore();
    await store.grantServer({
      serverName: 'docs',
      serverIdentity: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
      maxAutonomy: 'medium',
    });

    const denied = await runZeroMcpPermissions(dir, ['clear', '--json'], permissionPath);

    expect(denied.exitCode).toBe(1);
    expect(denied.stderr).toContain('--confirm');
    expect(await store.list()).toHaveLength(1);

    const cleared = await runZeroMcpPermissions(dir, ['clear', '--confirm', '--json'], permissionPath);

    expect(cleared.exitCode).toBe(0);
    expect(cleared.stderr.trim()).toBe('');
    expect(JSON.parse(cleared.stdout)).toEqual({ cleared: 1 });
    expect(await store.list()).toEqual([]);
  });
});
