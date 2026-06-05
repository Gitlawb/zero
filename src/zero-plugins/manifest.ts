import { existsSync, realpathSync } from 'fs';
import { basename, dirname, isAbsolute, relative, resolve, sep, win32 } from 'path';
import { z } from 'zod';
import type {
  ZeroLoadedPlugin,
  ZeroPluginHookEvent,
  ZeroPluginSource,
  ZeroPluginToolPermission,
} from './types';

export interface ParseZeroPluginManifestOptions {
  source: ZeroPluginSource;
  root: string;
  pluginDir: string;
  manifestPath: string;
  allowManifestToolAutoApproval?: boolean;
}

const PluginIdSchema = z.string().trim().min(1).regex(
  /^[A-Za-z0-9][A-Za-z0-9._-]*$/,
  'Use letters, numbers, dots, dashes, or underscores.'
);
const PluginNameSchema = z.string().trim().min(1);
const JsonObjectSchema = z.record(z.string(), z.unknown());
const PluginToolPermissionSchema = z.enum(['allow', 'prompt', 'deny']);
const PluginHookEventSchema = z.enum(['beforeTool', 'afterTool', 'sessionStart', 'sessionEnd']);

const ZeroPluginToolExtensionSchema = z.object({
  name: PluginIdSchema,
  description: z.string().trim().min(1).optional(),
  command: z.string().trim().min(1),
  args: z.array(z.string()).optional(),
  inputSchema: JsonObjectSchema.optional(),
  permission: PluginToolPermissionSchema.optional(),
});

const ZeroPluginPathExtensionSchema = z.object({
  name: PluginIdSchema,
  description: z.string().trim().min(1).optional(),
  path: z.string().trim().min(1),
});

const ZeroPluginHookExtensionSchema = z.object({
  name: PluginIdSchema,
  description: z.string().trim().min(1).optional(),
  event: PluginHookEventSchema,
  command: z.string().trim().min(1),
  args: z.array(z.string()).optional(),
});

export const ZeroPluginManifestSchema = z.object({
  schemaVersion: z.literal(1),
  id: PluginIdSchema,
  name: PluginNameSchema,
  version: z.string().trim().min(1),
  description: z.string().trim().min(1).optional(),
  enabled: z.boolean().optional(),
  tools: z.array(ZeroPluginToolExtensionSchema).optional(),
  prompts: z.array(ZeroPluginPathExtensionSchema).optional(),
  skills: z.array(ZeroPluginPathExtensionSchema).optional(),
  hooks: z.array(ZeroPluginHookExtensionSchema).optional(),
});

export function parseZeroPluginManifest(
  manifest: unknown,
  options: ParseZeroPluginManifestOptions
): ZeroLoadedPlugin {
  const parsed = ZeroPluginManifestSchema.parse(manifest);
  const pluginDir = resolve(options.pluginDir);

  return {
    schemaVersion: 1,
    id: parsed.id,
    name: parsed.name,
    version: parsed.version,
    description: parsed.description,
    enabled: parsed.enabled ?? true,
    source: options.source,
    root: resolve(options.root),
    pluginDir,
    manifestPath: resolve(options.manifestPath),
    tools: (parsed.tools ?? []).map((tool) => {
      const permission = normalizePluginToolPermission(
        tool.permission,
        options.allowManifestToolAutoApproval
      );
      return {
        name: tool.name,
        description: tool.description,
        command: tool.command,
        args: tool.args ?? [],
        inputSchema: tool.inputSchema ?? {
          type: 'object',
          properties: {},
          additionalProperties: true,
        },
        permission,
      };
    }),
    prompts: (parsed.prompts ?? []).map((prompt) => ({
      name: prompt.name,
      description: prompt.description,
      path: resolvePluginPath(pluginDir, prompt.path, `prompts.${prompt.name}.path`),
    })),
    skills: (parsed.skills ?? []).map((skill) => ({
      name: skill.name,
      description: skill.description,
      path: resolvePluginPath(pluginDir, skill.path, `skills.${skill.name}.path`),
    })),
    hooks: (parsed.hooks ?? []).map((hook) => ({
      name: hook.name,
      description: hook.description,
      event: hook.event as ZeroPluginHookEvent,
      command: hook.command,
      args: hook.args ?? [],
    })),
  };
}

function resolvePluginPath(pluginDir: string, value: string, fieldPath: string): string {
  if (isAbsolute(value) || win32.isAbsolute(value)) {
    throw new Error(`${fieldPath} must stay inside the plugin directory.`);
  }

  const pluginRoot = resolve(pluginDir);
  const pluginRootCheck = resolveSymlinkAwarePath(pluginRoot);
  const resolved = resolve(pluginDir, value);
  const resolvedCheck = resolveSymlinkAwarePath(resolved);
  const pathWithinPlugin = relative(pluginRootCheck, resolvedCheck);
  const rootBoundary = pluginRootCheck.endsWith(sep) ? pluginRootCheck : `${pluginRootCheck}${sep}`;
  if (
    pathWithinPlugin === '' ||
    isAbsolute(pathWithinPlugin) ||
    win32.isAbsolute(pathWithinPlugin) ||
    pathWithinPlugin.startsWith('..') ||
    pathWithinPlugin.includes('..\\') ||
    (resolvedCheck !== pluginRootCheck && !resolvedCheck.startsWith(rootBoundary))
  ) {
    throw new Error(`${fieldPath} must stay inside the plugin directory.`);
  }
  return resolved;
}

function resolveSymlinkAwarePath(path: string): string {
  try {
    return realpathSync(path);
  } catch (error) {
    if (!isENOENT(error)) {
      throw error;
    }
  }

  const missing: string[] = [];
  let existing = path;
  while (!existsSync(existing)) {
    const parent = dirname(existing);
    if (parent === existing) {
      return path;
    }
    missing.unshift(basename(existing));
    existing = parent;
  }

  try {
    return resolve(realpathSync(existing), ...missing);
  } catch (error) {
    if (!isENOENT(error)) {
      throw error;
    }
    return path;
  }
}

function isENOENT(error: unknown): boolean {
  return typeof error === 'object' && error !== null && 'code' in error && error.code === 'ENOENT';
}

function normalizePluginToolPermission(
  permission: ZeroPluginToolPermission | undefined,
  allowManifestToolAutoApproval = false
): ZeroPluginToolPermission {
  const resolved = (permission ?? 'prompt') as ZeroPluginToolPermission;
  if (resolved === 'allow' && !allowManifestToolAutoApproval) {
    return 'prompt';
  }
  return resolved;
}
