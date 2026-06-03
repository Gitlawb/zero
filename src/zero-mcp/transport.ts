import { spawn, type ChildProcessWithoutNullStreams } from 'child_process';
import {
  ZERO_MCP_PROTOCOL_VERSION,
  type ZeroMcpCallResult,
  type ZeroMcpHttpServerConfig,
  type ZeroMcpJsonRpcResponse,
  type ZeroMcpRemoteTool,
  type ZeroMcpServerConfig,
  type ZeroMcpStdioServerConfig,
  type ZeroMcpTransportClient,
} from './types';
import { normalizeZeroMcpServerConfig } from './config';
import { ZERO_VERSION } from '../version';

export class ZeroMcpError extends Error {
  constructor(message: string, options?: ErrorOptions) {
    super(message, options);
    this.name = 'ZeroMcpError';
  }
}

export class ZeroMcpJsonRpcError extends ZeroMcpError {
  constructor(
    public readonly code: number,
    message: string,
    public readonly data?: unknown
  ) {
    super(message);
    this.name = 'ZeroMcpJsonRpcError';
  }
}

interface PendingRequest {
  resolve: (value: unknown) => void;
  reject: (err: Error) => void;
  timer: ReturnType<typeof setTimeout>;
}

export interface ZeroMcpTransportOptions {
  timeoutMs?: number;
}

const DEFAULT_TIMEOUT_MS = 5000;

export function createZeroMcpTransport(
  serverName: string,
  config: ZeroMcpServerConfig,
  options: ZeroMcpTransportOptions = {}
): ZeroMcpTransportClient {
  const normalized = normalizeZeroMcpServerConfig(config);
  if (normalized.type === 'stdio') {
    return new ZeroMcpStdioTransport(serverName, normalized, options);
  }

  return new ZeroMcpHttpTransport(serverName, normalized, options);
}

export class ZeroMcpStdioTransport implements ZeroMcpTransportClient {
  private child: ChildProcessWithoutNullStreams | undefined;
  private initialized: Promise<void> | undefined;
  private stdoutBuffer = '';
  private stderrBuffer = '';
  private nextId = 1;
  private readonly pending = new Map<string | number, PendingRequest>();
  private readonly timeoutMs: number;

  constructor(
    private readonly serverName: string,
    private readonly config: ZeroMcpStdioServerConfig,
    options: ZeroMcpTransportOptions = {}
  ) {
    this.timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS;
  }

  async listTools(): Promise<ZeroMcpRemoteTool[]> {
    await this.ensureInitialized();
    const result = await this.request('tools/list', {});
    return normalizeToolsListResult(result, this.serverName);
  }

  async callTool(name: string, args: Record<string, unknown>): Promise<ZeroMcpCallResult> {
    await this.ensureInitialized();
    const result = await this.request('tools/call', {
      name,
      arguments: args,
    });
    return normalizeToolCallResult(result, this.serverName, name);
  }

  async close(): Promise<void> {
    const child = this.child;
    this.child = undefined;
    this.initialized = undefined;
    if (!child) return;

    child.stdin.end();
    if (!child.killed) {
      child.kill();
    }
  }

  private async ensureInitialized(): Promise<void> {
    if (!this.initialized) {
      this.initialized = (async () => {
        await this.request('initialize', {
          protocolVersion: ZERO_MCP_PROTOCOL_VERSION,
          capabilities: {},
          clientInfo: {
            name: 'zero',
            version: ZERO_VERSION,
          },
        });
        this.notify('notifications/initialized', {});
      })();
    }

    await this.initialized;
  }

  private ensureStarted(): ChildProcessWithoutNullStreams {
    if (this.child) return this.child;

    const child = spawn(this.config.command, this.config.args ?? [], {
      cwd: this.config.cwd,
      env: {
        ...process.env,
        ...(this.config.env ?? {}),
      },
      stdio: ['pipe', 'pipe', 'pipe'],
    });

    child.stdout.setEncoding('utf-8');
    child.stderr.setEncoding('utf-8');

    child.stdout.on('data', (chunk: string) => this.handleStdout(chunk));
    child.stderr.on('data', (chunk: string) => {
      this.stderrBuffer = (this.stderrBuffer + chunk).slice(-4000);
    });
    child.on('error', (err) => this.rejectAll(err));
    child.on('exit', (code, signal) => {
      this.child = undefined;
      this.initialized = undefined;
      if (this.pending.size > 0) {
        this.rejectAll(new ZeroMcpError(
          `MCP server "${this.serverName}" exited before responding` +
            ` (code ${code ?? 'null'}, signal ${signal ?? 'null'}).` +
            (this.stderrBuffer ? ` stderr: ${this.stderrBuffer.trim()}` : '')
        ));
      }
    });

    this.child = child;
    return child;
  }

  private async request(method: string, params: unknown): Promise<unknown> {
    const child = this.ensureStarted();
    const id = this.nextId++;

    const promise = new Promise<unknown>((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new ZeroMcpError(
          `MCP request "${method}" to "${this.serverName}" timed out after ${this.timeoutMs}ms.`
        ));
      }, this.timeoutMs);
      this.pending.set(id, { resolve, reject, timer });
    });

    child.stdin.write(JSON.stringify({
      jsonrpc: '2.0',
      id,
      method,
      params,
    }) + '\n');

    return promise;
  }

  private notify(method: string, params: unknown): void {
    const child = this.ensureStarted();
    child.stdin.write(JSON.stringify({
      jsonrpc: '2.0',
      method,
      params,
    }) + '\n');
  }

  private handleStdout(chunk: string): void {
    this.stdoutBuffer += chunk;
    let newlineIndex = this.stdoutBuffer.indexOf('\n');
    while (newlineIndex >= 0) {
      const rawLine = this.stdoutBuffer.slice(0, newlineIndex).trim();
      this.stdoutBuffer = this.stdoutBuffer.slice(newlineIndex + 1);
      if (rawLine) this.handleMessage(rawLine);
      newlineIndex = this.stdoutBuffer.indexOf('\n');
    }
  }

  private handleMessage(rawLine: string): void {
    let message: ZeroMcpJsonRpcResponse;
    try {
      message = JSON.parse(rawLine) as ZeroMcpJsonRpcResponse;
    } catch (err: unknown) {
      this.rejectAll(new ZeroMcpError(
        `MCP server "${this.serverName}" wrote invalid JSON to stdout.`,
        { cause: err }
      ));
      return;
    }

    if (!('id' in message)) return;
    const pending = this.pending.get(message.id ?? '');
    if (!pending) return;

    clearTimeout(pending.timer);
    this.pending.delete(message.id ?? '');

    if ('error' in message) {
      pending.reject(new ZeroMcpJsonRpcError(
        message.error.code,
        message.error.message,
        message.error.data
      ));
      return;
    }

    pending.resolve(message.result);
  }

  private rejectAll(err: Error): void {
    for (const pending of this.pending.values()) {
      clearTimeout(pending.timer);
      pending.reject(err);
    }
    this.pending.clear();
  }
}

export class ZeroMcpHttpTransport implements ZeroMcpTransportClient {
  private initialized = false;
  private sessionId: string | undefined;
  private nextId = 1;
  private readonly timeoutMs: number;

  constructor(
    private readonly serverName: string,
    private readonly config: ZeroMcpHttpServerConfig,
    options: ZeroMcpTransportOptions = {}
  ) {
    this.timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS;
  }

  async listTools(): Promise<ZeroMcpRemoteTool[]> {
    await this.ensureInitialized();
    const result = await this.request('tools/list', {});
    return normalizeToolsListResult(result, this.serverName);
  }

  async callTool(name: string, args: Record<string, unknown>): Promise<ZeroMcpCallResult> {
    await this.ensureInitialized();
    const result = await this.request('tools/call', {
      name,
      arguments: args,
    });
    return normalizeToolCallResult(result, this.serverName, name);
  }

  private async ensureInitialized(): Promise<void> {
    if (this.initialized) return;

    await this.request('initialize', {
      protocolVersion: ZERO_MCP_PROTOCOL_VERSION,
      capabilities: {},
      clientInfo: {
        name: 'zero',
        version: ZERO_VERSION,
      },
    }, { skipInitialize: true });
    await this.notify('notifications/initialized', {});
    this.initialized = true;
  }

  private async request(
    method: string,
    params: unknown,
    options: { skipInitialize?: boolean } = {}
  ): Promise<unknown> {
    if (!options.skipInitialize) {
      await this.ensureInitialized();
    }

    const id = this.nextId++;
    const response = await this.postJsonRpc({
      jsonrpc: '2.0',
      id,
      method,
      params,
    });
    return response;
  }

  private async notify(method: string, params: unknown): Promise<void> {
    await this.postJsonRpc({
      jsonrpc: '2.0',
      method,
      params,
    });
  }

  private async postJsonRpc(payload: Record<string, unknown>): Promise<unknown> {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), this.timeoutMs);
    try {
      const response = await fetch(this.config.url, {
        method: 'POST',
        headers: {
          ...this.config.headers,
          'content-type': 'application/json',
          accept: 'application/json, text/event-stream',
          'mcp-protocol-version': ZERO_MCP_PROTOCOL_VERSION,
          ...(this.sessionId ? { 'mcp-session-id': this.sessionId } : {}),
        },
        body: JSON.stringify(payload),
        signal: controller.signal,
      });

      const nextSessionId = response.headers.get('mcp-session-id');
      if (nextSessionId) this.sessionId = nextSessionId;

      if (response.status === 202) return undefined;
      if (!response.ok) {
        throw new ZeroMcpError(
          `MCP HTTP server "${this.serverName}" returned ${response.status} ${response.statusText}.`
        );
      }

      const contentType = response.headers.get('content-type') ?? '';
      if (contentType.includes('text/event-stream')) {
        return extractJsonRpcResultFromSse(await response.text());
      }

      return extractJsonRpcResult(await response.json());
    } catch (err: unknown) {
      if (err instanceof Error && err.name === 'AbortError') {
        throw new ZeroMcpError(
          `MCP HTTP request to "${this.serverName}" timed out after ${this.timeoutMs}ms.`
        );
      }
      throw err;
    } finally {
      clearTimeout(timeout);
    }
  }
}

function normalizeToolsListResult(result: unknown, serverName: string): ZeroMcpRemoteTool[] {
  if (!isRecord(result) || !Array.isArray(result.tools)) {
    throw new ZeroMcpError(`MCP server "${serverName}" returned an invalid tools/list result.`);
  }

  return result.tools.map((tool) => {
    if (!isRecord(tool) || typeof tool.name !== 'string' || tool.name.trim() === '') {
      throw new ZeroMcpError(`MCP server "${serverName}" returned a tool without a valid name.`);
    }

    return {
      name: tool.name,
      description: typeof tool.description === 'string' ? tool.description : undefined,
      inputSchema: isRecord(tool.inputSchema) ? tool.inputSchema : undefined,
    };
  });
}

function normalizeToolCallResult(
  result: unknown,
  serverName: string,
  toolName: string
): ZeroMcpCallResult {
  if (!isRecord(result)) {
    throw new ZeroMcpError(
      `MCP tool "${serverName}/${toolName}" returned an invalid tools/call result.`
    );
  }

  return {
    content: Array.isArray(result.content) ? result.content as ZeroMcpCallResult['content'] : undefined,
    structuredContent: result.structuredContent,
    isError: typeof result.isError === 'boolean' ? result.isError : undefined,
  };
}

function extractJsonRpcResultFromSse(text: string): unknown {
  const dataLines = text
    .split(/\r?\n/)
    .filter((line) => line.startsWith('data:'))
    .map((line) => line.slice('data:'.length).trim())
    .filter((line) => line.length > 0 && line !== '[DONE]');

  for (const data of dataLines) {
    const parsed = JSON.parse(data);
    if (isRecord(parsed) && 'id' in parsed) {
      return extractJsonRpcResult(parsed);
    }
  }

  throw new ZeroMcpError('MCP SSE response did not include a JSON-RPC response.');
}

function extractJsonRpcResult(response: unknown): unknown {
  if (!isRecord(response)) {
    throw new ZeroMcpError('MCP response was not a JSON-RPC object.');
  }

  if ('error' in response && isRecord(response.error)) {
    const code = typeof response.error.code === 'number' ? response.error.code : -32000;
    const message = typeof response.error.message === 'string'
      ? response.error.message
      : 'Unknown MCP JSON-RPC error';
    throw new ZeroMcpJsonRpcError(code, message, response.error.data);
  }

  return response.result;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}
