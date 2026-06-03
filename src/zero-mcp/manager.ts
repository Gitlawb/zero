import {
  computeZeroMcpServerIdentity,
  normalizeZeroMcpServerConfig,
  validateZeroMcpServerName,
} from './config';
import {
  type ZeroMcpCallResult,
  type ZeroMcpRemoteTool,
  type ZeroMcpServerConfig,
  type ZeroMcpServerStatus,
  type ZeroMcpToolDescriptor,
  type ZeroMcpTransportClient,
} from './types';
import {
  createZeroMcpTransport,
  type ZeroMcpTransportOptions,
} from './transport';

export interface ZeroMcpClientManagerOptions extends ZeroMcpTransportOptions {
  transportFactory?: (serverName: string, config: ZeroMcpServerConfig) => ZeroMcpTransportClient;
}

export class ZeroMcpClientManager {
  private readonly servers = new Map<string, ZeroMcpServerConfig>();
  private readonly transports = new Map<string, ZeroMcpTransportClient>();

  constructor(
    servers: Record<string, ZeroMcpServerConfig> = {},
    private readonly options: ZeroMcpClientManagerOptions = {}
  ) {
    for (const [name, config] of Object.entries(servers)) {
      this.setServer(name, config);
    }
  }

  setServer(name: string, config: ZeroMcpServerConfig): void {
    validateZeroMcpServerName(name);
    this.servers.set(name, normalizeZeroMcpServerConfig(config));
    const existing = this.transports.get(name);
    if (existing?.close) {
      void existing.close();
    }
    this.transports.delete(name);
  }

  listServers(): ZeroMcpServerStatus[] {
    return Array.from(this.servers.entries()).map(([name, config]) => ({
      name,
      type: config.type,
      identity: computeZeroMcpServerIdentity(config),
      enabled: config.enabled ?? true,
    }));
  }

  async listTools(serverName?: string): Promise<ZeroMcpToolDescriptor[]> {
    const names = serverName ? [serverName] : Array.from(this.servers.keys());
    const descriptors: ZeroMcpToolDescriptor[] = [];

    for (const name of names) {
      const config = this.requireServer(name);
      if (config.enabled === false) continue;

      const identity = computeZeroMcpServerIdentity(config);
      const transport = this.getTransport(name, config);
      const tools = await transport.listTools();
      descriptors.push(...tools.map((tool) => toDescriptor(name, identity, tool)));
    }

    return descriptors;
  }

  async callTool(
    serverName: string,
    toolName: string,
    args: Record<string, unknown>
  ): Promise<ZeroMcpCallResult> {
    const config = this.requireServer(serverName);
    if (config.enabled === false) {
      throw new Error(`MCP server "${serverName}" is disabled.`);
    }

    return this.getTransport(serverName, config).callTool(toolName, args);
  }

  async closeAll(): Promise<void> {
    await Promise.all(
      Array.from(this.transports.values()).map((transport) => transport.close?.() ?? Promise.resolve())
    );
    this.transports.clear();
  }

  private requireServer(name: string): ZeroMcpServerConfig {
    const config = this.servers.get(name);
    if (!config) {
      throw new Error(`Unknown MCP server "${name}".`);
    }
    return config;
  }

  private getTransport(name: string, config: ZeroMcpServerConfig): ZeroMcpTransportClient {
    const existing = this.transports.get(name);
    if (existing) return existing;

    const created = this.options.transportFactory
      ? this.options.transportFactory(name, config)
      : createZeroMcpTransport(name, config, { timeoutMs: this.options.timeoutMs });
    this.transports.set(name, created);
    return created;
  }
}

function toDescriptor(
  serverName: string,
  serverIdentity: string,
  tool: ZeroMcpRemoteTool
): ZeroMcpToolDescriptor {
  return {
    ...tool,
    serverName,
    serverIdentity,
    inputSchema: tool.inputSchema ?? {
      type: 'object',
      properties: {},
      additionalProperties: true,
    },
  };
}
