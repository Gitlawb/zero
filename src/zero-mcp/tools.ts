import { createHash } from 'crypto';
import { z } from 'zod';
import type { Tool } from '../tools/types';
import type { ToolRegistry } from '../tools/registry';
import type {
  ZeroMcpCallResult,
  ZeroMcpToolContent,
  ZeroMcpToolDescriptor,
} from './types';
import type { ZeroMcpClientManager } from './manager';

export function createZeroMcpToolName(
  serverName: string,
  serverIdentity: string,
  toolName: string
): string {
  return [
    'mcp',
    sanitizeToolSegment(serverName),
    sanitizeIdentitySegment(serverIdentity),
    sanitizeToolSegment(toolName),
    shortToolDigest(toolName),
  ].join('__');
}

export function createZeroMcpTool(
  descriptor: ZeroMcpToolDescriptor,
  manager: ZeroMcpClientManager
): Tool & { toJSONSchema: () => Record<string, unknown> } {
  return {
    name: createZeroMcpToolName(descriptor.serverName, descriptor.serverIdentity, descriptor.name),
    description: descriptor.description
      ? `[MCP:${descriptor.serverName}] ${descriptor.description}`
      : `[MCP:${descriptor.serverName}] ${descriptor.name}`,
    parameters: z.object({}).catchall(z.unknown()),
    safety: {
      sideEffect: 'network',
      permission: 'prompt',
      reason: `Calls MCP tool "${descriptor.name}" on server "${descriptor.serverName}".`,
    },
    zeroMcp: {
      serverName: descriptor.serverName,
      serverIdentity: descriptor.serverIdentity,
      toolName: descriptor.name,
    },
    toJSONSchema: () => normalizeMcpInputSchema(descriptor.inputSchema),
    async execute(args) {
      const result = await manager.callTool(descriptor.serverName, descriptor.name, args);
      return formatZeroMcpCallResult(result);
    },
  };
}

export async function registerZeroMcpTools(
  registry: ToolRegistry,
  manager: ZeroMcpClientManager,
  options: { serverName?: string } = {}
): Promise<Tool[]> {
  const descriptors = await manager.listTools(options.serverName);
  const tools = descriptors.map((descriptor) => createZeroMcpTool(descriptor, manager));
  const names = new Set<string>();
  for (const tool of tools) {
    if (names.has(tool.name) || registry.get(tool.name)) {
      throw new Error(`Duplicate MCP tool registry name "${tool.name}" was refused.`);
    }
    names.add(tool.name);
  }
  for (const tool of tools) {
    registry.register(tool);
  }
  return tools;
}

export function formatZeroMcpCallResult(result: ZeroMcpCallResult): string {
  const parts: string[] = [];

  for (const item of result.content ?? []) {
    const formatted = formatContentItem(item);
    if (formatted) parts.push(formatted);
  }

  if (result.structuredContent !== undefined) {
    parts.push(JSON.stringify(result.structuredContent, null, 2));
  }

  if (parts.length === 0) {
    return result.isError ? 'MCP tool returned an error without content.' : 'MCP tool completed.';
  }

  return parts.join('\n');
}

export function normalizeMcpInputSchema(schema: Record<string, unknown>): Record<string, unknown> {
  if (schema.type !== 'object') {
    return {
      type: 'object',
      properties: {},
      additionalProperties: true,
    };
  }

  return {
    ...schema,
    properties: isRecord(schema.properties) ? schema.properties : {},
    additionalProperties: schema.additionalProperties ?? true,
  };
}

function formatContentItem(item: ZeroMcpToolContent): string | undefined {
  if (isRecord(item) && item.type === 'text' && typeof item.text === 'string') {
    return item.text;
  }

  if (isRecord(item) && item.type === 'resource') {
    return JSON.stringify(item.resource, null, 2);
  }

  if (isRecord(item) && item.type === 'image') {
    const mime = typeof item.mimeType === 'string' ? item.mimeType : 'image/*';
    return `[MCP image content: ${mime}]`;
  }

  return JSON.stringify(item, null, 2);
}

function sanitizeToolSegment(value: string): string {
  const sanitized = value
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9_]+/g, '_')
    .replace(/^_+|_+$/g, '');
  return sanitized || 'unnamed';
}

function sanitizeIdentitySegment(value: string): string {
  const sanitized = value
    .trim()
    .toLowerCase()
    .replace(/[^a-f0-9]+/g, '')
    .slice(0, 12);
  return sanitized || 'unknown';
}

function shortToolDigest(value: string): string {
  return createHash('sha256').update(value).digest('hex').slice(0, 8);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}
