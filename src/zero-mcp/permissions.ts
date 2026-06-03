import { mkdir, readFile, rename, rm, writeFile } from 'fs/promises';
import { homedir } from 'os';
import { dirname, isAbsolute, join, resolve } from 'path';
import { validateZeroMcpServerName } from './config';

export type ZeroMcpPermissionScope = 'server' | 'tool';
export type ZeroMcpPermissionAutonomy = 'low' | 'medium' | 'high';

export interface ZeroMcpPermissionGrant {
  scope: ZeroMcpPermissionScope;
  serverName: string;
  serverIdentity: string;
  toolName?: string;
  maxAutonomy: ZeroMcpPermissionAutonomy;
  approvedAt: string;
}

export interface ZeroMcpPermissionStoreOptions {
  filePath?: string;
  now?: () => Date;
  env?: NodeJS.ProcessEnv;
}

export interface GrantZeroMcpServerPermissionInput {
  serverName: string;
  serverIdentity: string;
  maxAutonomy?: ZeroMcpPermissionAutonomy;
}

export interface GrantZeroMcpToolPermissionInput extends GrantZeroMcpServerPermissionInput {
  toolName: string;
}

export interface CheckZeroMcpToolPermissionInput {
  serverName: string;
  serverIdentity: string;
  toolName: string;
  requestedAutonomy?: ZeroMcpPermissionAutonomy;
}

interface StoredZeroMcpGrant {
  serverIdentity: string;
  maxAutonomy: ZeroMcpPermissionAutonomy;
  approvedAt: string;
}

interface ZeroMcpPermissionFile {
  schemaVersion: 1;
  servers: Record<string, StoredZeroMcpGrant>;
  tools: Record<string, Record<string, StoredZeroMcpGrant>>;
}

const ZERO_MCP_PERMISSION_SCHEMA_VERSION = 1;
const ZERO_MCP_PERMISSION_AUTONOMY_ORDER: Record<ZeroMcpPermissionAutonomy, number> = {
  low: 0,
  medium: 1,
  high: 2,
};

export function resolveZeroMcpPermissionPath(env: NodeJS.ProcessEnv = process.env): string {
  const override = env.ZERO_MCP_PERMISSIONS_PATH?.trim();
  if (override) {
    return isAbsolute(override) ? override : resolve(override);
  }

  const configHome = env.XDG_CONFIG_HOME?.trim();
  const baseDir = configHome
    ? (isAbsolute(configHome) ? configHome : resolve(configHome))
    : join(homedir(), '.config');
  return join(baseDir, 'zero', 'mcp-permissions.json');
}

export class ZeroMcpPermissionStore {
  readonly filePath: string;
  private readonly now: () => Date;
  private writeQueue: Promise<unknown> = Promise.resolve();

  constructor(options: ZeroMcpPermissionStoreOptions = {}) {
    this.filePath = options.filePath ?? resolveZeroMcpPermissionPath(options.env);
    this.now = options.now ?? (() => new Date());
  }

  async grantServer(input: GrantZeroMcpServerPermissionInput): Promise<ZeroMcpPermissionGrant> {
    const grant = createStoredGrant(input.serverIdentity, input.maxAutonomy, this.now);
    validateZeroMcpServerName(input.serverName);

    return this.withWriteLock(async () => {
      const state = await this.readState();
      state.servers[input.serverName] = grant;
      await this.writeState(state);
      return toServerGrant(input.serverName, grant);
    });
  }

  async grantTool(input: GrantZeroMcpToolPermissionInput): Promise<ZeroMcpPermissionGrant> {
    const grant = createStoredGrant(input.serverIdentity, input.maxAutonomy, this.now);
    validateZeroMcpServerName(input.serverName);
    validateToolName(input.toolName);

    return this.withWriteLock(async () => {
      const state = await this.readState();
      state.tools[input.serverName] = state.tools[input.serverName] ?? {};
      state.tools[input.serverName]![input.toolName] = grant;
      await this.writeState(state);
      return toToolGrant(input.serverName, input.toolName, grant);
    });
  }

  async isToolPersistentlyApproved(input: CheckZeroMcpToolPermissionInput): Promise<boolean> {
    validateZeroMcpServerName(input.serverName);
    validateToolName(input.toolName);
    const requestedAutonomy = normalizeAutonomy(input.requestedAutonomy ?? 'low');
    const state = await this.readState();
    const toolGrant = state.tools[input.serverName]?.[input.toolName];
    if (isGrantAllowed(toolGrant, input.serverIdentity, requestedAutonomy)) {
      return true;
    }

    const serverGrant = state.servers[input.serverName];
    return isGrantAllowed(serverGrant, input.serverIdentity, requestedAutonomy);
  }

  async list(): Promise<ZeroMcpPermissionGrant[]> {
    const state = await this.readState();
    const servers = Object.entries(state.servers)
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([serverName, grant]) => toServerGrant(serverName, grant));
    const tools = Object.entries(state.tools)
      .flatMap(([serverName, serverTools]) =>
        Object.entries(serverTools).map(([toolName, grant]) => toToolGrant(serverName, toolName, grant))
      )
      .sort((left, right) =>
        left.serverName.localeCompare(right.serverName) ||
        (left.toolName ?? '').localeCompare(right.toolName ?? '')
      );
    return [...servers, ...tools];
  }

  async revokeTool(serverName: string, toolName: string): Promise<number> {
    validateZeroMcpServerName(serverName);
    validateToolName(toolName);

    return this.withWriteLock(async () => {
      const state = await this.readState();
      const serverTools = state.tools[serverName];
      if (!serverTools?.[toolName]) return 0;

      delete serverTools[toolName];
      if (Object.keys(serverTools).length === 0) {
        delete state.tools[serverName];
      }
      await this.writeState(state);
      return 1;
    });
  }

  async revokeServer(serverName: string): Promise<number> {
    validateZeroMcpServerName(serverName);

    return this.withWriteLock(async () => {
      const state = await this.readState();
      let revoked = state.servers[serverName] ? 1 : 0;
      delete state.servers[serverName];

      const serverTools = state.tools[serverName];
      if (serverTools) {
        revoked += Object.keys(serverTools).length;
        delete state.tools[serverName];
      }

      if (revoked > 0) {
        await this.writeState(state);
      }
      return revoked;
    });
  }

  async clear(): Promise<number> {
    return this.withWriteLock(async () => {
      const state = await this.readState();
      const count = countGrants(state);
      if (count > 0) {
        await this.writeState(createEmptyState());
      }
      return count;
    });
  }

  private async readState(): Promise<ZeroMcpPermissionFile> {
    let text: string;
    try {
      text = await readFile(this.filePath, 'utf-8');
    } catch (err: any) {
      if (err?.code === 'ENOENT') return createEmptyState();
      throw err;
    }

    try {
      return parsePermissionFile(JSON.parse(text), this.filePath);
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err);
      throw new Error(`Invalid MCP permissions file at ${this.filePath}: ${message}`);
    }
  }

  private async writeState(state: ZeroMcpPermissionFile): Promise<void> {
    await mkdir(dirname(this.filePath), { recursive: true });
    const tempPath = `${this.filePath}.tmp-${process.pid}-${Date.now()}-${Math.random().toString(16).slice(2)}`;

    try {
      await writeFile(tempPath, `${JSON.stringify(state, null, 2)}\n`, 'utf-8');
      await rename(tempPath, this.filePath);
    } catch (err: unknown) {
      await rm(tempPath, { force: true });
      throw err;
    }
  }

  private async withWriteLock<T>(operation: () => Promise<T>): Promise<T> {
    const next = this.writeQueue.then(operation, operation);
    this.writeQueue = next.then(
      () => undefined,
      () => undefined
    );
    return next;
  }
}

function createEmptyState(): ZeroMcpPermissionFile {
  return {
    schemaVersion: ZERO_MCP_PERMISSION_SCHEMA_VERSION,
    servers: {},
    tools: {},
  };
}

function createStoredGrant(
  serverIdentity: string,
  maxAutonomy: ZeroMcpPermissionAutonomy | undefined,
  now: () => Date
): StoredZeroMcpGrant {
  const identity = serverIdentity.trim();
  if (!identity) {
    throw new Error('MCP server identity is required.');
  }

  return {
    serverIdentity: identity,
    maxAutonomy: normalizeAutonomy(maxAutonomy ?? 'low'),
    approvedAt: now().toISOString(),
  };
}

function parsePermissionFile(parsed: unknown, filePath: string): ZeroMcpPermissionFile {
  if (!isRecord(parsed)) {
    throw new Error('Expected a JSON object.');
  }
  if (parsed.schemaVersion !== ZERO_MCP_PERMISSION_SCHEMA_VERSION) {
    throw new Error(`Unsupported schemaVersion in ${filePath}.`);
  }

  return {
    schemaVersion: ZERO_MCP_PERMISSION_SCHEMA_VERSION,
    servers: parseGrantRecord(parsed.servers, 'servers'),
    tools: parseToolGrantRecord(parsed.tools),
  };
}

function parseGrantRecord(value: unknown, label: string): Record<string, StoredZeroMcpGrant> {
  if (value === undefined) return {};
  if (!isRecord(value)) {
    throw new Error(`Expected "${label}" to be an object.`);
  }

  return Object.fromEntries(
    Object.entries(value).map(([name, grant]) => [name, parseStoredGrant(grant, `${label}.${name}`)])
  );
}

function parseToolGrantRecord(value: unknown): Record<string, Record<string, StoredZeroMcpGrant>> {
  if (value === undefined) return {};
  if (!isRecord(value)) {
    throw new Error('Expected "tools" to be an object.');
  }

  return Object.fromEntries(
    Object.entries(value).map(([serverName, serverTools]) => [
      serverName,
      parseGrantRecord(serverTools, `tools.${serverName}`),
    ])
  );
}

function parseStoredGrant(value: unknown, label: string): StoredZeroMcpGrant {
  if (!isRecord(value)) {
    throw new Error(`Expected "${label}" to be an object.`);
  }
  const serverIdentity = readString(value.serverIdentity, `${label}.serverIdentity`);
  const approvedAt = readString(value.approvedAt, `${label}.approvedAt`);
  return {
    serverIdentity,
    approvedAt,
    maxAutonomy: normalizeAutonomy(readString(value.maxAutonomy, `${label}.maxAutonomy`)),
  };
}

function readString(value: unknown, label: string): string {
  if (typeof value !== 'string' || value.trim().length === 0) {
    throw new Error(`Expected "${label}" to be a non-empty string.`);
  }
  return value.trim();
}

function normalizeAutonomy(value: string): ZeroMcpPermissionAutonomy {
  const normalized = value.trim().toLowerCase();
  if (normalized === 'low' || normalized === 'medium' || normalized === 'high') {
    return normalized;
  }
  throw new Error(`Invalid MCP permission autonomy "${value}". Expected low, medium, or high.`);
}

function validateToolName(toolName: string): void {
  if (!toolName.trim()) {
    throw new Error('MCP tool name is required.');
  }
}

function isGrantAllowed(
  grant: StoredZeroMcpGrant | undefined,
  serverIdentity: string,
  requestedAutonomy: ZeroMcpPermissionAutonomy
): boolean {
  if (!grant) return false;
  if (grant.serverIdentity !== serverIdentity) return false;
  return ZERO_MCP_PERMISSION_AUTONOMY_ORDER[requestedAutonomy] <=
    ZERO_MCP_PERMISSION_AUTONOMY_ORDER[grant.maxAutonomy];
}

function toServerGrant(serverName: string, grant: StoredZeroMcpGrant): ZeroMcpPermissionGrant {
  return {
    scope: 'server',
    serverName,
    serverIdentity: grant.serverIdentity,
    maxAutonomy: grant.maxAutonomy,
    approvedAt: grant.approvedAt,
  };
}

function toToolGrant(
  serverName: string,
  toolName: string,
  grant: StoredZeroMcpGrant
): ZeroMcpPermissionGrant {
  return {
    scope: 'tool',
    serverName,
    serverIdentity: grant.serverIdentity,
    toolName,
    maxAutonomy: grant.maxAutonomy,
    approvedAt: grant.approvedAt,
  };
}

function countGrants(state: ZeroMcpPermissionFile): number {
  return Object.keys(state.servers).length +
    Object.values(state.tools).reduce((count, serverTools) => count + Object.keys(serverTools).length, 0);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}
