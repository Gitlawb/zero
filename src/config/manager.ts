import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'fs';
import { join } from 'path';
import { homedir } from 'os';
import type { ZeroConfig, ProviderProfile } from './types';

const CONFIG_DIR = join(homedir(), '.config', 'zero');
const CONFIG_PATH = join(CONFIG_DIR, 'config.json');

function ensureConfigDir() {
  if (!existsSync(CONFIG_DIR)) {
    mkdirSync(CONFIG_DIR, { recursive: true });
  }
}

function readConfig(): ZeroConfig {
  ensureConfigDir();

  if (!existsSync(CONFIG_PATH)) {
    return { providers: [] };
  }

  try {
    const content = readFileSync(CONFIG_PATH, 'utf-8');
    const parsed = JSON.parse(content);
    return {
      activeProvider: parsed.activeProvider,
      providers: parsed.providers ?? [],
    };
  } catch {
    return { providers: [] };
  }
}

function writeConfig(config: ZeroConfig) {
  ensureConfigDir();
  writeFileSync(CONFIG_PATH, JSON.stringify(config, null, 2), 'utf-8');
}

export class ConfigManager {
  private config: ZeroConfig;

  constructor() {
    this.config = readConfig();
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
  getEffectiveProviderConfig(): {
    provider?: ProviderProfile['provider'];
    baseURL: string;
    apiKey?: string;
    model: string;
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
      };
    }

    // Fallback to env vars
    if (process.env.OPENAI_BASE_URL || process.env.OPENAI_MODEL) {
      return {
        provider: 'openai',
        baseURL: process.env.OPENAI_BASE_URL || 'https://api.openai.com/v1',
        apiKey: process.env.OPENAI_API_KEY,
        model: process.env.OPENAI_MODEL || 'gpt-4o',
      };
    }

    return null;
  }
}

// Singleton for simplicity
export const configManager = new ConfigManager();
