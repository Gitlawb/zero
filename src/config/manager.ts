import { existsSync, mkdirSync, readFileSync, renameSync, writeFileSync } from 'fs';
import { join } from 'path';
import { homedir } from 'os';
import { ZERO_DEFAULT_MODEL_ID } from '../zero-model-registry';
import {
  type ProviderProfile,
  type ZeroConfig,
  ZeroConfigSchema,
} from './loader';
import {
  parseProviderProfileKind,
  type EffectiveProviderConfig,
} from './types';

const CONFIG_DIR = join(homedir(), '.config', 'zero');
const CONFIG_PATH = join(CONFIG_DIR, 'config.json');
type ConfigState = ZeroConfig & { providers: ProviderProfile[] };

function ensureConfigDir() {
  if (!existsSync(CONFIG_DIR)) {
    mkdirSync(CONFIG_DIR, { recursive: true });
  }
}

function normalizeConfig(config: ZeroConfig): ConfigState {
  return {
    ...config,
    providers: config.providers ?? [],
  };
}

function readConfig(): ConfigState {
  ensureConfigDir();

  if (!existsSync(CONFIG_PATH)) {
    return { providers: [] };
  }

  try {
    const content = readFileSync(CONFIG_PATH, 'utf-8');
    const parsed = JSON.parse(content);
    return normalizeConfig(ZeroConfigSchema.partial().parse(parsed));
  } catch (err: any) {
    console.warn(
      `[zero] Ignoring invalid config at ${CONFIG_PATH}: ${err?.message ?? String(err)}`
    );
    return { providers: [] };
  }
}

function writeConfig(config: ConfigState) {
  ensureConfigDir();
  const tempPath = `${CONFIG_PATH}.tmp`;
  writeFileSync(tempPath, JSON.stringify(config, null, 2), 'utf-8');
  renameSync(tempPath, CONFIG_PATH);
}

export class ConfigManager {
  private config: ConfigState;

  constructor(initialConfig?: ZeroConfig) {
    this.config = initialConfig ? normalizeConfig(initialConfig) : readConfig();
  }

  getActiveProvider(): ProviderProfile | undefined {
    if (!this.config.activeProvider) return undefined;
    return this.config.providers.find(p => p.name === this.config.activeProvider);
  }

  setActiveProvider(name: string): boolean {
    const exists = this.config.providers.some(p => p.name === name);
    if (!exists) return false;

    this.config.activeProvider = name;
    writeConfig(this.config);
    return true;
  }

  listProviders(): ProviderProfile[] {
    return [...this.config.providers];
  }

  getProvider(name: string): ProviderProfile | undefined {
    return this.config.providers.find(p => p.name === name);
  }

  addProvider(profile: ProviderProfile): void {
    // Remove if exists (update)
    this.config.providers = this.config.providers.filter(p => p.name !== profile.name);
    this.config.providers.push(profile);

    // If this is the first provider, make it active
    if (!this.config.activeProvider) {
      this.config.activeProvider = profile.name;
    }

    writeConfig(this.config);
  }

  removeProvider(name: string): boolean {
    const before = this.config.providers.length;
    this.config.providers = this.config.providers.filter(p => p.name !== name);

    if (this.config.activeProvider === name) {
      this.config.activeProvider = this.config.providers[0]?.name;
    }

    writeConfig(this.config);
    return this.config.providers.length < before;
  }

  // Used by the agent loop
  getEffectiveProviderConfig(env: NodeJS.ProcessEnv = process.env): {
    provider?: EffectiveProviderConfig['provider'];
    baseURL: string;
    apiKey?: string;
    model: string;
    source: EffectiveProviderConfig['source'];
    profileName?: string;
  } | null {
    // Highest priority: provider command (handled elsewhere)
    // Then: active profile from config
    const active = this.getActiveProvider();
    if (active) {
      return {
        provider: active.provider,
        baseURL: active.baseURL,
        apiKey: active.apiKey,
        model: active.model,
        source: 'profile',
        profileName: active.name,
      };
    }

    // Fallback to env vars
    if (env.OPENAI_API_KEY || env.OPENAI_BASE_URL || env.OPENAI_MODEL) {
      return {
        provider: parseProviderProfileKind(env.ZERO_PROVIDER),
        baseURL: env.OPENAI_BASE_URL || 'https://api.openai.com/v1',
        apiKey: env.OPENAI_API_KEY,
        model: env.OPENAI_MODEL || ZERO_DEFAULT_MODEL_ID,
        source: 'environment',
      };
    }

    return null;
  }
}

// Singleton for simplicity
export const configManager = new ConfigManager();
