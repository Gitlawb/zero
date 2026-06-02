export const PROVIDER_PROFILE_KINDS = [
  'openai',
  'anthropic',
  'google',
  'openai-compatible',
] as const;

export type ProviderProfileKind = (typeof PROVIDER_PROFILE_KINDS)[number];

export function parseProviderProfileKind(
  value: unknown
): ProviderProfileKind | undefined {
  if (value === undefined || value === null || value === '') return undefined;
  const normalized = typeof value === 'string' ? value.trim().toLowerCase() : value;
  if (
    typeof normalized === 'string' &&
    (PROVIDER_PROFILE_KINDS as readonly string[]).includes(normalized)
  ) {
    return normalized as ProviderProfileKind;
  }
  throw new Error(`Unknown Zero provider kind: ${String(value)}`);
}

export type ProviderConfigSource = 'profile' | 'provider-command' | 'environment';

export interface EffectiveProviderConfig {
  provider?: ProviderProfileKind;
  apiKey?: string;
  baseURL: string;
  model: string;
  source: ProviderConfigSource;
  profileName?: string;
}

export type { ProviderProfile, ZeroConfig } from './loader';
