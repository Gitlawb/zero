import { existsSync } from 'fs';
import { mkdir, readFile, rename, writeFile } from 'fs/promises';
import { homedir } from 'os';
import { dirname, join, resolve } from 'path';
import { z, ZodError } from 'zod';
import type {
  SelectZeroHooksInput,
  ZeroHookConfigSource,
  ZeroHookDefinition,
  ZeroHookDiagnostic,
  ZeroHookLoadResult,
  ZeroHookPaths,
  ZeroHooksConfig,
} from './types';

const HookIdSchema = z.string().trim().min(1).regex(
  /^[A-Za-z0-9][A-Za-z0-9._-]*$/,
  'Use letters, numbers, dots, dashes, or underscores.'
);
const HookEventSchema = z.enum(['beforeTool', 'afterTool', 'sessionStart', 'sessionEnd']);

const SESSION_HOOK_EVENTS = new Set(['sessionStart', 'sessionEnd']);

const HookDefinitionSchema = z.object({
  id: HookIdSchema,
  name: z.string().trim().min(1).optional(),
  description: z.string().trim().min(1).optional(),
  enabled: z.boolean().optional(),
  event: HookEventSchema,
  matcher: z.string().trim().min(1).optional(),
  command: z.string().trim().min(1),
  args: z.array(z.string()).optional(),
}).superRefine((hook, ctx) => {
  if (hook.matcher && SESSION_HOOK_EVENTS.has(hook.event)) {
    ctx.addIssue({
      code: z.ZodIssueCode.custom,
      path: ['matcher'],
      message: 'matcher is only supported for beforeTool and afterTool hooks.',
    });
  }
});

const HooksConfigSchema = z.object({
  enabled: z.boolean().optional(),
  hooks: z.array(HookDefinitionSchema).optional(),
});

interface HookLayer {
  source: ZeroHookConfigSource;
  path: string;
  config: ZeroHooksConfig;
}

export interface ResolveZeroHookPathOptions {
  cwd?: string;
  env?: NodeJS.ProcessEnv;
}

export interface LoadZeroHooksConfigOptions extends ResolveZeroHookPathOptions {
  userConfigPath?: string;
  projectConfigPath?: string;
}

export interface ZeroHookConfigStoreOptions {
  configPath?: string;
}

export function resolveZeroHookPaths(options: ResolveZeroHookPathOptions = {}): ZeroHookPaths {
  const env = options.env ?? process.env;
  const cwd = resolve(options.cwd ?? process.cwd());
  const home = env.HOME?.trim() || env.USERPROFILE?.trim() || homedir();
  const configHome = env.XDG_CONFIG_HOME?.trim() || join(home, '.config');
  const dataHome = env.XDG_DATA_HOME?.trim() || join(home, '.local', 'share');

  return {
    userConfigPath: join(configHome, 'zero', 'hooks.json'),
    projectConfigPath: join(cwd, '.zero', 'hooks.json'),
    auditPath: join(dataHome, 'zero', 'hooks', 'audit.jsonl'),
  };
}

export async function loadZeroHooksConfig(
  options: LoadZeroHooksConfigOptions = {}
): Promise<ZeroHookLoadResult> {
  const paths = resolveZeroHookPaths(options);
  const userConfigPath = resolve(options.userConfigPath ?? paths.userConfigPath);
  const projectConfigPath = resolve(options.projectConfigPath ?? paths.projectConfigPath);
  const diagnostics: ZeroHookDiagnostic[] = [];
  const layers: HookLayer[] = [];

  for (const [source, path] of [
    ['user', userConfigPath],
    ['project', projectConfigPath],
  ] as const) {
    const layer = await readHookLayer(source, path, diagnostics);
    if (layer) layers.push(layer);
  }

  return {
    config: mergeHookLayers(layers, diagnostics),
    diagnostics,
    paths: {
      ...paths,
      userConfigPath,
      projectConfigPath,
    },
  };
}

export async function writeZeroHooksConfig(path: string, config: Partial<ZeroHooksConfig>): Promise<void> {
  const normalized = normalizeHooksConfig(config);
  const resolved = resolve(path);
  await mkdir(dirname(resolved), { recursive: true });
  const tempPath = `${resolved}.tmp`;
  await writeFile(tempPath, JSON.stringify(normalized, null, 2), 'utf-8');
  await rename(tempPath, resolved);
}

export class ZeroHookConfigStore {
  private static readonly mutationQueues = new Map<string, Promise<void>>();
  private readonly configPath: string;

  constructor(options: ZeroHookConfigStoreOptions = {}) {
    this.configPath = resolve(options.configPath ?? resolveZeroHookPaths().projectConfigPath);
  }

  async list(): Promise<ZeroHooksConfig> {
    const result = await loadZeroHooksConfig({
      userConfigPath: `${this.configPath}.user-missing`,
      projectConfigPath: this.configPath,
    });
    return result.config;
  }

  async upsert(hook: Omit<ZeroHookDefinition, 'enabled'> & { enabled?: boolean }): Promise<ZeroHookDefinition> {
    return await this.withMutationLock(async () => {
      const config = await this.list();
      const normalized = normalizeHookDefinition(hook);
      const nextHooks = config.hooks.filter((existing) => existing.id !== normalized.id);
      nextHooks.push(normalized);
      await writeZeroHooksConfig(this.configPath, {
        enabled: config.enabled,
        hooks: nextHooks,
      });
      return normalized;
    });
  }

  async remove(hookId: string): Promise<boolean> {
    return await this.withMutationLock(async () => {
      const config = await this.list();
      const nextHooks = config.hooks.filter((hook) => hook.id !== hookId);
      if (nextHooks.length === config.hooks.length) return false;
      await writeZeroHooksConfig(this.configPath, {
        enabled: config.enabled,
        hooks: nextHooks,
      });
      return true;
    });
  }

  async setEnabled(hookId: string, enabled: boolean): Promise<boolean> {
    return await this.withMutationLock(async () => {
      const config = await this.list();
      let changed = false;
      const nextHooks = config.hooks.map((hook) => {
        if (hook.id !== hookId) return hook;
        changed = true;
        return { ...hook, enabled };
      });
      if (!changed) return false;
      await writeZeroHooksConfig(this.configPath, {
        enabled: config.enabled,
        hooks: nextHooks,
      });
      return true;
    });
  }

  private async withMutationLock<T>(fn: () => Promise<T>): Promise<T> {
    const previous = ZeroHookConfigStore.mutationQueues.get(this.configPath) ?? Promise.resolve();
    let release!: () => void;
    const current = new Promise<void>((resolveCurrent) => {
      release = resolveCurrent;
    });
    const next = previous.then(() => current, () => current);
    ZeroHookConfigStore.mutationQueues.set(this.configPath, next);

    await previous.catch(() => undefined);
    try {
      return await fn();
    } finally {
      release();
      if (ZeroHookConfigStore.mutationQueues.get(this.configPath) === next) {
        ZeroHookConfigStore.mutationQueues.delete(this.configPath);
      }
    }
  }
}

export function selectZeroHooks(
  config: ZeroHooksConfig,
  input: SelectZeroHooksInput
): ZeroHookDefinition[] {
  if (!config.enabled) return [];

  return config.hooks.filter((hook) => {
    if (!hook.enabled || hook.event !== input.event) return false;
    if (!hook.matcher) return true;
    if (!input.toolName) return false;
    return matchesHookMatcher(hook.matcher, input.toolName);
  });
}

export function formatZeroHookList(
  config: ZeroHooksConfig,
  diagnostics: ZeroHookDiagnostic[]
): string {
  const lines: string[] = [];
  lines.push(`Zero Hooks: ${config.enabled ? 'enabled' : 'disabled'}`);

  if (config.hooks.length === 0) {
    lines.push('  No hooks configured.');
  } else {
    for (const hook of config.hooks) {
      const matcher = hook.matcher ? ` ${hook.matcher}` : '';
      const command = [hook.command, ...hook.args].join(' ');
      lines.push(
        `  ${hook.id} [${hook.event}${matcher}] ${hook.enabled ? 'enabled' : 'disabled'} - ${command}`
      );
    }
  }

  if (diagnostics.length > 0) {
    lines.push('Hook diagnostics:');
    for (const diagnostic of diagnostics) {
      lines.push(`  [${diagnostic.kind}] ${diagnostic.message}`);
    }
  }

  return lines.join('\n');
}

async function readHookLayer(
  source: ZeroHookConfigSource,
  path: string,
  diagnostics: ZeroHookDiagnostic[]
): Promise<HookLayer | undefined> {
  if (!existsSync(path)) return undefined;

  let parsed: unknown;
  try {
    parsed = JSON.parse(await readFile(path, 'utf-8'));
  } catch (err: unknown) {
    diagnostics.push({
      kind: err instanceof SyntaxError ? 'json' : 'io',
      source,
      path,
      message: getErrorMessage(err),
    });
    return undefined;
  }

  try {
    return {
      source,
      path,
      config: normalizeHooksConfig(parsed),
    };
  } catch (err: unknown) {
    diagnostics.push(toHookDiagnostic(err, source, path));
    return undefined;
  }
}

function mergeHookLayers(
  layers: HookLayer[],
  diagnostics: ZeroHookDiagnostic[]
): ZeroHooksConfig {
  let enabled = true;
  const byId = new Map<string, ZeroHookDefinition>();
  const sourceById = new Map<string, HookLayer>();

  for (const layer of layers) {
    enabled = layer.config.enabled;
    for (const hook of layer.config.hooks) {
      const previous = sourceById.get(hook.id);
      if (previous) {
        diagnostics.push({
          kind: 'duplicate',
          source: layer.source,
          path: layer.path,
          hookId: hook.id,
          message: `Hook "${hook.id}" from ${layer.source} overrides ${previous.source} hook at ${previous.path}.`,
        });
      }
      byId.set(hook.id, hook);
      sourceById.set(hook.id, layer);
    }
  }

  return {
    enabled,
    hooks: Array.from(byId.values()).sort((left, right) => left.id.localeCompare(right.id)),
  };
}

function normalizeHooksConfig(value: unknown): ZeroHooksConfig {
  const parsed = HooksConfigSchema.parse(value ?? {});
  return {
    enabled: parsed.enabled ?? true,
    hooks: (parsed.hooks ?? [])
      .map(normalizeHookDefinition)
      .sort((left, right) => left.id.localeCompare(right.id)),
  };
}

function normalizeHookDefinition(value: z.input<typeof HookDefinitionSchema>): ZeroHookDefinition {
  const parsed = HookDefinitionSchema.parse(value);
  return {
    id: parsed.id,
    name: parsed.name,
    description: parsed.description,
    event: parsed.event,
    matcher: parsed.matcher,
    command: parsed.command,
    args: parsed.args ?? [],
    enabled: parsed.enabled ?? true,
  };
}

function matchesHookMatcher(matcher: string, toolName: string): boolean {
  if (matcher === '*') return true;
  if (!matcher.includes('*')) return matcher === toolName;

  const segments = matcher.split('*');
  let cursor = 0;
  let searchEnd = toolName.length;

  if (!matcher.startsWith('*')) {
    const first = segments.shift() ?? '';
    if (!toolName.startsWith(first)) return false;
    cursor = first.length;
  }

  if (!matcher.endsWith('*')) {
    const last = segments.pop() ?? '';
    if (!toolName.endsWith(last)) return false;
    searchEnd = toolName.length - last.length;
  }

  for (const segment of segments) {
    if (!segment) continue;
    const index = toolName.indexOf(segment, cursor);
    if (index === -1 || index + segment.length > searchEnd) return false;
    cursor = index + segment.length;
  }

  return cursor <= searchEnd;
}

function toHookDiagnostic(
  err: unknown,
  source: ZeroHookConfigSource,
  path: string
): ZeroHookDiagnostic {
  if (err instanceof ZodError) {
    const issue = err.issues[0];
    return {
      kind: 'schema',
      source,
      path,
      fieldPath: issue?.path.map(String).join('.'),
      message: issue?.message ?? err.message,
    };
  }

  return {
    kind: 'schema',
    source,
    path,
    message: getErrorMessage(err),
  };
}

function getErrorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
