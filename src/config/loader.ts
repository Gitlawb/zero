import { existsSync, readFileSync } from 'fs';
import { join } from 'path';
import { homedir } from 'os';
import { z } from 'zod';
import { PROVIDER_PROFILE_KINDS } from './types';
import { ZeroMcpConfigSchema, normalizeZeroMcpConfig } from '../zero-mcp/config';

/**
 * Layered configuration loader for Zero.
 *
 * Resolution order (highest priority last):
 *   1. Built-in defaults
 *   2. User config:     ~/.config/zero/config.json
 *   3. Project config:  <cwd>/.zero/config.json
 *   4. Environment:     ZERO_*  /  OPENAI_*  variables
 *   5. CLI flags (passed in explicitly to the loader)
 *
 * Anything the user or project explicitly sets wins over defaults and
 * environment variables. CLI flags win over everything except defaults.
 */

export const ProviderProfileSchema = z.object({
  name: z.string().min(1),
  provider: z.enum(PROVIDER_PROFILE_KINDS).optional(),
  baseURL: z.string().url(),
  apiKey: z.string().optional(),
  model: z.string().min(1),
  description: z.string().optional(),
});

export const ZeroConfigSchema = z.object({
  activeProvider: z.string().optional(),
  providers: z.array(ProviderProfileSchema).optional(),
  mcp: ZeroMcpConfigSchema.optional(),
  maxTurns: z.number().int().min(1).max(100).optional(),
  planMode: z.boolean().optional(),
  debug: z.boolean().optional(),
});

export type ZeroConfig = z.infer<typeof ZeroConfigSchema>;
export type ProviderProfile = z.infer<typeof ProviderProfileSchema>;

const DEFAULT_CONFIG: ZeroConfig = {
  providers: [],
  maxTurns: 12,
  planMode: false,
  debug: false,
};

export type ConfigLayerSource = 'defaults' | 'user' | 'project' | 'env' | 'cli';
export type ConfigLayerStatus = 'loaded' | 'invalid';
export type ConfigDiagnosticKind = 'io' | 'json' | 'schema' | 'env';

export interface ConfigDiagnostic {
  source: ConfigLayerSource;
  kind: ConfigDiagnosticKind;
  message: string;
  path?: string;
  fieldPath?: string;
}

export interface ConfigLayer {
  source: ConfigLayerSource;
  status: ConfigLayerStatus;
  path?: string;
  config: Partial<ZeroConfig>;
  errors?: ConfigDiagnostic[];
}

export interface LoadConfigOptions {
  /** Path to the project config (defaults to `<cwd>/.zero/config.json`). */
  projectConfigPath?: string;
  /** Path to the user config (defaults to `~/.config/zero/config.json`). */
  userConfigPath?: string;
  /** CLI flag overrides applied last. */
  cliOverrides?: Partial<ZeroConfig>;
  /** Read environment variables from this object (defaults to `process.env`). */
  env?: NodeJS.ProcessEnv;
}

const userConfigPath = (): string => join(homedir(), '.config', 'zero', 'config.json');
const projectConfigPath = (): string => join(process.cwd(), '.zero', 'config.json');

/**
 * Read and parse a single config file. Invalid files are returned as
 * diagnostics so a single bad file never blocks startup.
 */
export function readConfigLayer(
  source: Extract<ConfigLayerSource, 'user' | 'project'>,
  path: string
): ConfigLayer | undefined {
  if (!existsSync(path)) return undefined;

  let text: string;
  try {
    text = readFileSync(path, 'utf-8');
  } catch (err: unknown) {
    return invalidConfigLayer(source, path, [{
      source,
      kind: 'io',
      path,
      message: errorMessage(err),
    }]);
  }

  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch (err: unknown) {
    return invalidConfigLayer(source, path, [{
      source,
      kind: 'json',
      path,
      message: errorMessage(err),
    }]);
  }

  const result = ZeroConfigSchema.partial().safeParse(parsed);
  if (!result.success) {
    return invalidConfigLayer(
      source,
      path,
      result.error.issues.map((issue) => ({
        source,
        kind: 'schema',
        path,
        fieldPath: formatConfigFieldPath(issue.path),
        message: issue.message,
      }))
    );
  }

  return {
    source,
    status: 'loaded',
    path,
    config: result.data,
  };
}

function invalidConfigLayer(
  source: Extract<ConfigLayerSource, 'user' | 'project'>,
  path: string,
  errors: ConfigDiagnostic[]
): ConfigLayer {
  return {
    source,
    status: 'invalid',
    path,
    config: {},
    errors,
  };
}

function formatConfigFieldPath(path: PropertyKey[]): string | undefined {
  if (path.length === 0) return undefined;
  return path.map((part) => String(part)).join('.');
}

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

function envLayer(env: NodeJS.ProcessEnv = process.env): {
  config: ZeroConfig;
  diagnostics: ConfigDiagnostic[];
} {
  const layer: ZeroConfig = {};
  const diagnostics: ConfigDiagnostic[] = [];

  if (env.ZERO_MAX_TURNS) {
    const n = parseInt(env.ZERO_MAX_TURNS, 10);
    if (!Number.isNaN(n)) {
      layer.maxTurns = n;
    } else {
      diagnostics.push({
        source: 'env',
        kind: 'env',
        fieldPath: 'ZERO_MAX_TURNS',
        message: `ZERO_MAX_TURNS must be an integer, received "${env.ZERO_MAX_TURNS}".`,
      });
    }
  }
  if (env.ZERO_PLAN_MODE === '1' || env.ZERO_PLAN_MODE === 'true') layer.planMode = true;
  if (env.ZERO_DEBUG === '1' || env.ZERO_DEBUG === 'true') layer.debug = true;
  return { config: layer, diagnostics };
}

/**
 * Merge a sequence of layers, later ones winning. Built-in defaults
 * always come first. `providers` is concatenated across layers (later
 * entries with the same name replace earlier ones) so partial config
 * files can still contribute providers without clobbering the list.
 */
export function mergeLayers(...layers: ZeroConfig[]): ZeroConfig {
  const result: ZeroConfig = { providers: [] };
  for (const layer of layers) {
    if (layer.providers && layer.providers.length > 0) {
      for (const profile of layer.providers) {
        const idx = result.providers!.findIndex((p) => p.name === profile.name);
        if (idx >= 0) {
          result.providers![idx] = { ...result.providers![idx], ...profile };
        } else {
          result.providers!.push(profile);
        }
      }
    }
    if (layer.activeProvider !== undefined) result.activeProvider = layer.activeProvider;
    if (layer.mcp !== undefined) {
      const normalized = normalizeZeroMcpConfig(layer.mcp);
      result.mcp = {
        ...result.mcp,
        servers: {
          ...(result.mcp?.servers ?? {}),
          ...(normalized.servers ?? {}),
        },
      };
    }
    if (layer.maxTurns !== undefined) result.maxTurns = layer.maxTurns;
    if (layer.planMode !== undefined) result.planMode = layer.planMode;
    if (layer.debug !== undefined) result.debug = layer.debug;
  }
  return result;
}

/**
 * Load the full effective config and report every layer that contributed
 * a value. Useful for debugging and for `/config` style introspection.
 */
export function loadConfigWithLayers(options: LoadConfigOptions = {}): {
  effective: ZeroConfig;
  layers: ConfigLayer[];
  diagnostics: ConfigDiagnostic[];
} {
  const userPath = options.userConfigPath ?? userConfigPath();
  const projectPath = options.projectConfigPath ?? projectConfigPath();

  const defaults: ZeroConfig = { ...DEFAULT_CONFIG };

  const user = readConfigLayer('user', userPath);
  const project = readConfigLayer('project', projectPath);
  const { config: env, diagnostics: envDiagnostics } = envLayer(options.env);
  const cli: ZeroConfig = options.cliOverrides ?? {};

  const layers: ConfigLayer[] = [
    { source: 'defaults', status: 'loaded', config: defaults },
    ...(user ? [user] : []),
    ...(project ? [project] : []),
    ...(Object.keys(env).length || envDiagnostics.length
      ? [{
          source: 'env' as const,
          status: Object.keys(env).length ? 'loaded' as const : 'invalid' as const,
          config: env,
          ...(envDiagnostics.length ? { errors: envDiagnostics } : {}),
        }]
      : []),
    ...(Object.keys(cli).length ? [{ source: 'cli' as const, status: 'loaded' as const, config: cli }] : []),
  ];

  const diagnostics = layers.flatMap((layer) => layer.errors ?? []);
  const effective = mergeLayers(
    ...(layers
      .filter((layer) => layer.status === 'loaded')
      .map((layer) => layer.config) as ZeroConfig[])
  );
  return { effective, layers, diagnostics };
}

/**
 * Convenience wrapper: returns just the effective merged config.
 */
export function loadConfig(options: LoadConfigOptions = {}): ZeroConfig {
  return loadConfigWithLayers(options).effective;
}
