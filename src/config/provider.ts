import { configManager } from './manager';
import type { ProviderType } from './types';

export interface ProviderConfig {
  provider?: ProviderType;
  apiKey?: string;
  baseURL: string;
  model: string;
}

/**
 * Loads the effective provider configuration.
 * Priority order:
 *   1. ZERO_PROVIDER_COMMAND (external command) - highest
 *   2. Active profile from config (set via /provider)
 *   3. OPENAI_* environment variables
 */
export async function loadProviderConfig(): Promise<ProviderConfig> {
  // 1. Highest priority: external provider command
  const providerCommand = process.env.ZERO_PROVIDER_COMMAND;
  if (providerCommand) {
    const { stdout } = await Bun.$`${providerCommand}`.quiet();
    const parsed = JSON.parse(stdout.toString());

    return {
      provider: normalizeProviderType(parsed.provider ?? parsed.provider_type ?? parsed.type),
      apiKey: parsed.api_key,
      baseURL: parsed.base_url || 'https://api.openai.com/v1',
      model: parsed.model,
    };
  }

  // 2. Active profile from saved config
  const fromProfile = configManager.getEffectiveProviderConfig();
  if (fromProfile) {
    return fromProfile;
  }

  // 3. Fallback to raw environment variables
  const envApiKey = process.env.OPENAI_API_KEY;
  const envBaseURL = process.env.OPENAI_BASE_URL || 'https://api.openai.com/v1';
  const envModel = process.env.OPENAI_MODEL || 'gpt-4o';

  // If we have no API key and no provider command, give a helpful error
  if (!envApiKey && !process.env.ZERO_PROVIDER_COMMAND) {
    throw new Error(
      'No LLM provider configured.\n\n' +
      'Please run /provider to add one, or set OPENAI_API_KEY environment variable.'
    );
  }

  return {
    provider: 'openai',
    apiKey: envApiKey,
    baseURL: envBaseURL,
    model: envModel,
  };
}

function normalizeProviderType(value: unknown): ProviderType | undefined {
  if (value === 'openai' || value === 'anthropic' || value === 'google' || value === 'openai-compatible') {
    return value;
  }
  return undefined;
}
