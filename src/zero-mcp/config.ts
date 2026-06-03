import { createHash } from 'crypto';
import { z } from 'zod';
import type {
  ZeroMcpConfig,
  ZeroMcpServerConfig,
  ZeroMcpStdioServerConfig,
} from './types';

const StringRecordSchema = z.record(z.string(), z.string());

export const ZeroMcpStdioServerConfigSchema = z.object({
  type: z.literal('stdio'),
  command: z.string().trim().min(1),
  args: z.array(z.string()).optional(),
  cwd: z.string().trim().min(1).optional(),
  env: StringRecordSchema.optional(),
  enabled: z.boolean().optional(),
});

export const ZeroMcpHttpServerConfigSchema = z.object({
  type: z.union([z.literal('http'), z.literal('sse')]),
  url: z.string().url(),
  headers: StringRecordSchema.optional(),
  enabled: z.boolean().optional(),
});

export const ZeroMcpServerConfigSchema = z.discriminatedUnion('type', [
  ZeroMcpStdioServerConfigSchema,
  ZeroMcpHttpServerConfigSchema,
]);

export const ZeroMcpConfigSchema = z.object({
  servers: z.record(z.string(), ZeroMcpServerConfigSchema).optional(),
});

export function normalizeZeroMcpServerConfig(config: ZeroMcpServerConfig): ZeroMcpServerConfig {
  if (config.type === 'stdio') {
    return {
      ...config,
      args: config.args ?? [],
      env: config.env ?? {},
      enabled: config.enabled ?? true,
    };
  }

  return {
    ...config,
    headers: config.headers ?? {},
    enabled: config.enabled ?? true,
  };
}

export function normalizeZeroMcpConfig(config: ZeroMcpConfig | undefined): ZeroMcpConfig {
  const servers = config?.servers;
  if (!servers) return {};

  return {
    servers: Object.fromEntries(
      Object.entries(servers).map(([name, server]) => [
        name,
        normalizeZeroMcpServerConfig(server),
      ])
    ),
  };
}

export function computeZeroMcpServerIdentity(config: ZeroMcpServerConfig): string {
  const normalized = normalizeZeroMcpServerConfig(config);
  const identityPayload = normalized.type === 'stdio'
    ? {
        type: normalized.type,
        command: normalized.command,
        args: (normalized as ZeroMcpStdioServerConfig).args ?? [],
      }
    : {
        type: normalized.type,
        url: normalized.url,
      };

  return createHash('sha256')
    .update(JSON.stringify(identityPayload))
    .digest('hex')
    .slice(0, 32);
}

export function validateZeroMcpServerName(name: string): void {
  if (!/^[A-Za-z0-9._-]+$/.test(name)) {
    throw new Error(
      `Invalid MCP server name "${name}". Use letters, numbers, dots, dashes, or underscores.`
    );
  }
}
