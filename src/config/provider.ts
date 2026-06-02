import { configManager } from './manager';
import { execa } from 'execa';
import { z } from 'zod';
import { ZERO_DEFAULT_MODEL_ID } from '../zero-model-registry';
import {
  parseProviderProfileKind,
  type EffectiveProviderConfig,
} from './types';

export type ProviderConfig = EffectiveProviderConfig;

const ProviderCommandOutputSchema = z.object({
  provider: z.string().optional(),
  provider_kind: z.string().optional(),
  api_key: z.string().optional(),
  base_url: z.string().optional(),
  model: z.string().optional(),
  model_id: z.string().optional(),
}).passthrough();

/**
 * Loads the effective provider configuration.
 * Priority order:
 *   1. ZERO_PROVIDER_COMMAND (external command) - highest
 *   2. Active profile from config (set via /provider)
 *   3. OPENAI_* environment variables
 *
 * ZERO_PROVIDER_COMMAND must print JSON with:
 *   { model | model_id, api_key?, base_url?, provider | provider_kind? }
 */
export async function loadProviderConfig(): Promise<ProviderConfig> {
  // 1. Highest priority: external provider command
  const providerCommand = process.env.ZERO_PROVIDER_COMMAND;
  if (providerCommand) {
    let stdout = '';
    try {
      const result = await execa(providerCommand, {
        shell: true,
        timeout: 5000,
      });
      stdout = result.stdout;
    } catch (err: any) {
      if (err.timedOut) {
        throw new Error('ZERO_PROVIDER_COMMAND timed out after 5 seconds');
      }
      throw new Error(
        `ZERO_PROVIDER_COMMAND failed: ${err?.shortMessage ?? err?.message ?? String(err)}`
      );
    }

    let parsed: z.infer<typeof ProviderCommandOutputSchema>;
    try {
      parsed = ProviderCommandOutputSchema.parse(JSON.parse(stdout));
    } catch (err: any) {
      throw new Error(
        `ZERO_PROVIDER_COMMAND returned invalid JSON: ${err?.message ?? String(err)}`
      );
    }

    const model = parsed.model ?? parsed.model_id;
    if (!model) {
      throw new Error('ZERO_PROVIDER_COMMAND output must include a model or model_id field');
    }

    return {
      provider: parseProviderProfileKind(parsed.provider || parsed.provider_kind),
      apiKey: parsed.api_key,
      baseURL: parsed.base_url || 'https://api.openai.com/v1',
      model,
      source: 'provider-command',
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
  const envModel = process.env.OPENAI_MODEL || ZERO_DEFAULT_MODEL_ID;

  // If we have no API key and no provider command, give a helpful error
  if (!envApiKey && !process.env.ZERO_PROVIDER_COMMAND) {
    throw new Error(
      'No LLM provider configured.\n\n' +
      'Please run /provider to add one, or set OPENAI_API_KEY environment variable.'
    );
  }

  return {
    provider: parseProviderProfileKind(process.env.ZERO_PROVIDER),
    apiKey: envApiKey,
    baseURL: envBaseURL,
    model: envModel,
    source: 'environment',
  };
}
