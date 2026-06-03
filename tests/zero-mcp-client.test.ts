import { afterEach, describe, expect, it } from 'bun:test';
import { mkdir, mkdtemp, rm, writeFile } from 'fs/promises';
import { join } from 'path';
import { tmpdir } from 'os';
import { ToolRegistry } from '../src/tools/registry';
import { loadConfig } from '../src/config/loader';
import {
  ZeroMcpClientManager,
  computeZeroMcpServerIdentity,
  createZeroMcpToolName,
  registerZeroMcpTools,
} from '../src/zero-mcp';

const tempDirs: string[] = [];

afterEach(async () => {
  await Promise.all(tempDirs.splice(0).map((dir) => rm(dir, { recursive: true, force: true })));
});

async function makeTempDir(): Promise<string> {
  const dir = await mkdtemp(join(tmpdir(), 'zero-mcp-'));
  tempDirs.push(dir);
  return dir;
}

async function writeFakeMcpServer(dir: string): Promise<string> {
  const serverPath = join(dir, 'fake-mcp-server.mjs');
  await writeFile(serverPath, `
import { createInterface } from 'node:readline';

const rl = createInterface({ input: process.stdin });

function send(id, result) {
  process.stdout.write(JSON.stringify({ jsonrpc: '2.0', id, result }) + '\\n');
}

rl.on('line', (line) => {
  const message = JSON.parse(line);
  if (message.method === 'initialize') {
    send(message.id, {
      protocolVersion: '2025-06-18',
      capabilities: { tools: {} },
      serverInfo: { name: 'fake-zero-mcp', version: '1.0.0' },
    });
    return;
  }
  if (message.method === 'notifications/initialized') return;
  if (message.method === 'tools/list') {
    send(message.id, {
      tools: [
        {
          name: 'lookup',
          description: 'Look up Zero docs',
          inputSchema: {
            type: 'object',
            properties: { query: { type: 'string' } },
            required: ['query'],
            additionalProperties: false,
          },
        },
      ],
    });
    return;
  }
  if (message.method === 'tools/call') {
    send(message.id, {
      content: [{ type: 'text', text: 'answer:' + message.params.arguments.query }],
      isError: false,
    });
    return;
  }
  process.stdout.write(JSON.stringify({
    jsonrpc: '2.0',
    id: message.id,
    error: { code: -32601, message: 'unknown method' },
  }) + '\\n');
});
`, 'utf-8');
  return serverPath;
}

async function runZeroMcp(
  cwd: string,
  args: string[],
  envOverrides: NodeJS.ProcessEnv = {}
): Promise<{ exitCode: number; stdout: string; stderr: string }> {
  const child = Bun.spawn([process.execPath, join(process.cwd(), 'src/index.ts'), 'mcp', ...args], {
    cwd,
    env: { ...process.env, ...envOverrides },
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

describe('Zero MCP client backend', () => {
  it('loads and merges MCP server config from project config', async () => {
    const dir = await makeTempDir();
    const projectConfigPath = join(dir, 'config.json');
    await writeFile(projectConfigPath, JSON.stringify({
      mcp: {
        servers: {
          docs: {
            type: 'stdio',
            command: 'node',
            args: ['server.js'],
            env: { ZERO_TEST: '1' },
          },
          remote: {
            type: 'http',
            url: 'https://example.com/mcp',
            headers: { Authorization: 'Bearer test' },
          },
        },
      },
    }), 'utf-8');

    const config = loadConfig({
      userConfigPath: join(dir, 'missing-user.json'),
      projectConfigPath,
      env: {},
    });

    expect(config.mcp?.servers?.docs).toMatchObject({
      type: 'stdio',
      command: 'node',
      args: ['server.js'],
      env: { ZERO_TEST: '1' },
    });
    expect(config.mcp?.servers?.remote).toMatchObject({
      type: 'http',
      url: 'https://example.com/mcp',
      headers: { Authorization: 'Bearer test' },
    });
  });

  it('computes stable server identities from transport-defining fields', () => {
    const first = computeZeroMcpServerIdentity({
      type: 'stdio',
      command: 'node',
      args: ['server.js'],
      env: { IGNORED_SECRET: 'x' },
    });
    const sameTransport = computeZeroMcpServerIdentity({
      type: 'stdio',
      command: 'node',
      args: ['server.js'],
      env: { IGNORED_SECRET: 'y' },
    });
    const changedTransport = computeZeroMcpServerIdentity({
      type: 'stdio',
      command: 'node',
      args: ['other.js'],
    });

    expect(first).toHaveLength(32);
    expect(first).toBe(sameTransport);
    expect(first).not.toBe(changedTransport);
  });

  it('discovers and calls tools from a stdio MCP server', async () => {
    const dir = await makeTempDir();
    const serverPath = await writeFakeMcpServer(dir);
    const manager = new ZeroMcpClientManager({
      docs: {
        type: 'stdio',
        command: process.execPath,
        args: [serverPath],
      },
    });

    try {
      const tools = await manager.listTools();
      expect(tools).toHaveLength(1);
      const [tool] = tools;
      if (!tool) throw new Error('Expected one discovered MCP tool.');
      expect(tool).toMatchObject({
        serverName: 'docs',
        name: 'lookup',
        description: 'Look up Zero docs',
      });
      expect(tool.serverIdentity).toHaveLength(32);

      const result = await manager.callTool('docs', 'lookup', { query: 'sessions' });
      expect(result.content).toEqual([{ type: 'text', text: 'answer:sessions' }]);
    } finally {
      await manager.closeAll();
    }
  });

  it('registers MCP tools into the Zero tool registry with prompt-gated execution', async () => {
    const dir = await makeTempDir();
    const serverPath = await writeFakeMcpServer(dir);
    const registry = new ToolRegistry();
    const manager = new ZeroMcpClientManager({
      docs: {
        type: 'stdio',
        command: process.execPath,
        args: [serverPath],
      },
    });

    try {
      const registered = await registerZeroMcpTools(registry, manager);
      const [registeredTool] = registered;
      if (!registeredTool) throw new Error('Expected one registered MCP tool.');
      const toolName = registeredTool.name;
      const tool = registry.get(toolName);

      expect(registered.map((tool) => tool.name)).toEqual([toolName]);
      expect(tool).toBeDefined();
      expect(tool?.safety).toMatchObject({
        sideEffect: 'network',
        permission: 'prompt',
      });
      expect(tool?.toJSONSchema?.()).toMatchObject({
        type: 'object',
        properties: { query: { type: 'string' } },
        required: ['query'],
        additionalProperties: false,
      });

      const blocked = await registry.run(toolName, { query: 'blocked' });
      expect(blocked).toContain('Permission required');

      const allowed = await registry.run(toolName, { query: 'allowed' }, { permissionGranted: true });
      expect(allowed).toBe('answer:allowed');
    } finally {
      await manager.closeAll();
    }
  });

  it('generates collision-safe registry names for sanitized server and tool names', () => {
    const serverCollisionA = createZeroMcpToolName(
      'docs-prod',
      'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
      'lookup-all'
    );
    const serverCollisionB = createZeroMcpToolName(
      'docs_prod',
      'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
      'lookup_all'
    );
    const toolCollisionA = createZeroMcpToolName(
      'docs',
      'cccccccccccccccccccccccccccccccc',
      'lookup-all'
    );
    const toolCollisionB = createZeroMcpToolName(
      'docs',
      'cccccccccccccccccccccccccccccccc',
      'lookup_all'
    );

    expect(serverCollisionA).not.toBe(serverCollisionB);
    expect(toolCollisionA).not.toBe(toolCollisionB);
    expect(serverCollisionA).toContain('__aaaaaaaaaaaa__');
    expect(serverCollisionB).toContain('__bbbbbbbbbbbb__');
  });

  it('refuses duplicate generated MCP registry names before overwriting tools', async () => {
    const registry = new ToolRegistry();
    const manager = {
      async listTools() {
        return [
          {
            serverName: 'docs',
            serverIdentity: 'dddddddddddddddddddddddddddddddd',
            name: 'lookup',
            inputSchema: { type: 'object', properties: {} },
          },
          {
            serverName: 'docs',
            serverIdentity: 'dddddddddddddddddddddddddddddddd',
            name: 'lookup',
            inputSchema: { type: 'object', properties: {} },
          },
        ];
      },
      async callTool() {
        return { content: [] };
      },
    } as unknown as ZeroMcpClientManager;

    await expect(registerZeroMcpTools(registry, manager)).rejects.toThrow(
      'Duplicate MCP tool registry name'
    );
    expect(registry.get(createZeroMcpToolName(
      'docs',
      'dddddddddddddddddddddddddddddddd',
      'lookup'
    ))).toBeUndefined();
  });

  it('lists configured MCP servers and discovered tools from the CLI', async () => {
    const dir = await makeTempDir();
    const serverPath = await writeFakeMcpServer(dir);
    await mkdir(join(dir, '.zero'), { recursive: true });
    await writeFile(join(dir, '.zero', 'config.json'), JSON.stringify({
      mcp: {
        servers: {
          docs: {
            type: 'stdio',
            command: process.execPath,
            args: [serverPath],
          },
        },
      },
    }), 'utf-8');

    const listResult = await runZeroMcp(dir, ['list', '--json'], {
      HOME: join(dir, 'home'),
      USERPROFILE: join(dir, 'home'),
    });

    expect(listResult.exitCode).toBe(0);
    expect(listResult.stderr.trim()).toBe('');
    expect(JSON.parse(listResult.stdout).servers).toEqual([
      expect.objectContaining({
        name: 'docs',
        type: 'stdio',
        enabled: true,
      }),
    ]);

    const toolsResult = await runZeroMcp(dir, ['list', '--tools', '--json'], {
      HOME: join(dir, 'home'),
      USERPROFILE: join(dir, 'home'),
    });

    expect(toolsResult.exitCode).toBe(0);
    expect(toolsResult.stderr.trim()).toBe('');
    expect(JSON.parse(toolsResult.stdout).tools).toEqual([
      expect.objectContaining({
        name: 'lookup',
        zeroToolName: expect.stringMatching(/^mcp__docs__[a-f0-9]{12}__lookup__[a-f0-9]{8}$/),
        serverName: 'docs',
      }),
    ]);
  });
});
