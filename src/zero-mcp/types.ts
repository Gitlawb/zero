export const ZERO_MCP_PROTOCOL_VERSION = '2025-06-18';

export type ZeroMcpTransportType = 'stdio' | 'http' | 'sse';

export interface ZeroMcpBaseServerConfig {
  type: ZeroMcpTransportType;
  enabled?: boolean;
}

export interface ZeroMcpStdioServerConfig extends ZeroMcpBaseServerConfig {
  type: 'stdio';
  command: string;
  args?: string[];
  cwd?: string;
  env?: Record<string, string>;
}

export interface ZeroMcpHttpServerConfig extends ZeroMcpBaseServerConfig {
  type: 'http' | 'sse';
  url: string;
  headers?: Record<string, string>;
}

export type ZeroMcpServerConfig = ZeroMcpStdioServerConfig | ZeroMcpHttpServerConfig;

export interface ZeroMcpConfig {
  servers?: Record<string, ZeroMcpServerConfig>;
}

export interface ZeroMcpServerStatus {
  name: string;
  type: ZeroMcpTransportType;
  identity: string;
  enabled: boolean;
}

export interface ZeroMcpRemoteTool {
  name: string;
  description?: string;
  inputSchema?: Record<string, unknown>;
}

export interface ZeroMcpToolDescriptor extends ZeroMcpRemoteTool {
  serverName: string;
  serverIdentity: string;
  inputSchema: Record<string, unknown>;
}

export interface ZeroMcpTextContent {
  type: 'text';
  text: string;
}

export interface ZeroMcpResourceContent {
  type: 'resource';
  resource: unknown;
}

export interface ZeroMcpImageContent {
  type: 'image';
  data?: string;
  mimeType?: string;
}

export type ZeroMcpToolContent =
  | ZeroMcpTextContent
  | ZeroMcpResourceContent
  | ZeroMcpImageContent
  | Record<string, unknown>;

export interface ZeroMcpCallResult {
  content?: ZeroMcpToolContent[];
  structuredContent?: unknown;
  isError?: boolean;
}

export interface ZeroMcpTransportClient {
  listTools(): Promise<ZeroMcpRemoteTool[]>;
  callTool(name: string, args: Record<string, unknown>): Promise<ZeroMcpCallResult>;
  close?(): Promise<void>;
}

export interface ZeroMcpJsonRpcSuccess {
  jsonrpc: '2.0';
  id: string | number;
  result: unknown;
}

export interface ZeroMcpJsonRpcFailure {
  jsonrpc: '2.0';
  id: string | number | null;
  error: {
    code: number;
    message: string;
    data?: unknown;
  };
}

export type ZeroMcpJsonRpcResponse = ZeroMcpJsonRpcSuccess | ZeroMcpJsonRpcFailure;
