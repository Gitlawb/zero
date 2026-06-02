import { existsSync, readFileSync } from 'fs';
import { join } from 'path';
import { homedir } from 'os';
import { z } from 'zod';
import { PROVIDER_PROFILE_KINDS } from './types';

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

export interface ConfigLayer {
  source: 'defaults' | 'user' | 'project' | 'env' | 'cli';
  config: Partial<ZeroConfig>;
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
 * Read and parse a single config file. Returns `{}` for any I/O or
 * parse error so a single bad file never blocks startup.
 */
function readConfigFile(path: string): ZeroConfig {
  if (!existsSync(path)) return {};
  try {
    const text = readFileSync(path, 'utf-8');
    const parsed = JSON.parse(text);
    return ZeroConfigSchema.partial().parse(parsed);
  } catch {
    return {};
  }
}

function envLayer(env: NodeJS.ProcessEnv = process.env): ZeroConfig {
  const layer: ZeroConfig = {};
  if (env.ZERO_MAX_TURNS) {
    const n = parseInt(env.ZERO_MAX_TURNS, 10);
    if (!Number.isNaN(n)) layer.maxTurns = n;
  }
  if (env.ZERO_PLAN_MODE === '1' || env.ZERO_PLAN_MODE === 'true') layer.planMode = true;
  if (env.ZERO_DEBUG === '1' || env.ZERO_DEBUG === 'true') layer.debug = true;
  return layer;
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
} {
  const userPath = options.userConfigPath ?? userConfigPath();
  const projectPath = options.projectConfigPath ?? projectConfigPath();

  const defaults: ZeroConfig = { ...DEFAULT_CONFIG };

  const user: ZeroConfig = readConfigFile(userPath);
  const project: ZeroConfig = readConfigFile(projectPath);
  const env: ZeroConfig = envLayer(options.env);
  const cli: ZeroConfig = options.cliOverrides ?? {};

  const layers: ConfigLayer[] = [
    { source: 'defaults', config: defaults },
    ...(Object.keys(user).length ? [{ source: 'user' as const, config: user }] : []),
    ...(Object.keys(project).length ? [{ source: 'project' as const, config: project }] : []),
    ...(Object.keys(env).length ? [{ source: 'env' as const, config: env }] : []),
    ...(Object.keys(cli).length ? [{ source: 'cli' as const, config: cli }] : []),
  ];

  const effective = mergeLayers(defaults, user, project, env, cli);
  return { effective, layers };
}

/**
 * Convenience wrapper: returns just the effective merged config.
 */
export function loadConfig(options: LoadConfigOptions = {}): ZeroConfig {
  return loadConfigWithLayers(options).effective;
}
