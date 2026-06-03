import {
  getZeroModel,
  listZeroModels,
} from './registry';
import type { ZeroModelDefinition, ZeroModelProvider } from './types';

export type ZeroModelProfileId = 'fast' | 'balanced' | 'deep' | 'cheap';

export interface ZeroModelProfile {
  id: ZeroModelProfileId;
  label: string;
  description: string;
  preferredModelIds: readonly string[];
  preferredProviders?: readonly ZeroModelProvider[];
}

export const ZERO_MODEL_PROFILES: readonly ZeroModelProfile[] = [
  {
    id: 'fast',
    label: 'Fast',
    description: 'Low-latency model for quick edit loops, summaries, and cheap checks.',
    preferredModelIds: ['gpt-4.1-mini', 'gemini-2.5-flash', 'claude-haiku-4.5'],
    preferredProviders: ['openai', 'google', 'anthropic'],
  },
  {
    id: 'balanced',
    label: 'Balanced',
    description: 'Default daily-driver profile for coding sessions with tools.',
    preferredModelIds: ['gpt-4.1', 'claude-sonnet-4.5', 'gemini-2.5-pro'],
    preferredProviders: ['openai', 'anthropic', 'google'],
  },
  {
    id: 'deep',
    label: 'Deep',
    description: 'Higher-capability profile for complex planning, refactors, and reviews.',
    preferredModelIds: ['claude-sonnet-4.5', 'claude-opus-4.1', 'gemini-2.5-pro', 'gpt-4.1'],
    preferredProviders: ['anthropic', 'google', 'openai'],
  },
  {
    id: 'cheap',
    label: 'Cheap',
    description: 'Lowest-cost profile for simple questions, search, and routing.',
    preferredModelIds: ['gpt-4.1-nano', 'gemini-2.5-flash-lite', 'gpt-4o-mini'],
    preferredProviders: ['openai', 'google'],
  },
];

export interface ZeroResolvedModelProfile {
  profile: ZeroModelProfile;
  model: ZeroModelDefinition;
}

export function listZeroModelProfiles(): readonly ZeroModelProfile[] {
  return ZERO_MODEL_PROFILES;
}

export function getZeroModelProfile(profileId: string): ZeroModelProfile | undefined {
  const normalized = normalizeProfileId(profileId);
  return ZERO_MODEL_PROFILES.find((profile) => profile.id === normalized);
}

export function isZeroModelProfile(profileId: string): profileId is ZeroModelProfileId {
  return getZeroModelProfile(profileId) !== undefined;
}

export function resolveZeroModelProfile(profileId: string): ZeroResolvedModelProfile | undefined {
  const profile = getZeroModelProfile(profileId);
  if (!profile) return undefined;

  for (const modelId of profile.preferredModelIds) {
    const model = getZeroModel(modelId);
    if (model && model.status !== 'deprecated') {
      return { profile, model };
    }
  }

  const fallback = listZeroModels()
    .filter((model) => model.status !== 'deprecated')
    .find((model) => !profile.preferredProviders || profile.preferredProviders.includes(model.provider));

  return fallback ? { profile, model: fallback } : undefined;
}

export function formatZeroModelProfile(profile: ZeroModelProfile): string {
  const resolved = resolveZeroModelProfile(profile.id);
  const model = resolved?.model.displayName ?? 'unavailable';
  return `${profile.id.padEnd(8)} ${profile.label.padEnd(8)} ${model} - ${profile.description}`;
}

function normalizeProfileId(value: string): ZeroModelProfileId | string {
  return value.trim().toLowerCase();
}
