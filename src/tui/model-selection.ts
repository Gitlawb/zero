import {
  getZeroModel,
  formatZeroModelProfile,
  listZeroModelProfiles,
  listZeroModels,
  resolveZeroModelProfile,
  resolveZeroModelId,
  type ZeroModelDefinition,
  type ZeroModelProvider,
  type ZeroModelProfile,
} from '../zero-model-registry';
import type { ProviderConfig } from '../config/provider';

export interface TuiModelStatus {
  modelId: string;
  label: string;
  providerLabel: string;
  sourceLabel: string;
  knownModel?: ZeroModelDefinition;
}

const PROVIDER_ORDER: ZeroModelProvider[] = ['openai', 'anthropic', 'google'];

export function getSelectableZeroModels(): ZeroModelDefinition[] {
  return listZeroModels().sort((a, b) => {
    const providerDelta = PROVIDER_ORDER.indexOf(a.provider) - PROVIDER_ORDER.indexOf(b.provider);
    if (providerDelta !== 0) return providerDelta;
    return a.displayName.localeCompare(b.displayName);
  });
}

export function resolveTuiModelSelection(input: string): ZeroModelDefinition | undefined {
  const normalized = input.trim();
  if (!normalized) return undefined;
  const profile = resolveZeroModelProfile(normalized);
  if (profile) return profile.model;
  const modelId = resolveZeroModelId(normalized);
  return modelId ? getZeroModel(modelId) : undefined;
}

export function resolveTuiModelProfileSelection(input: string): {
  profile: ZeroModelProfile;
  model: ZeroModelDefinition;
} | undefined {
  return resolveZeroModelProfile(input);
}

export function buildTuiModelStatus(
  providerConfig: Pick<ProviderConfig, 'model' | 'provider' | 'source' | 'profileName'> | undefined,
  sessionModelOverride?: string
): TuiModelStatus {
  const modelId = sessionModelOverride || providerConfig?.model || 'default';
  const knownModel = getZeroModel(modelId);

  return {
    modelId,
    label: knownModel?.displayName || modelId,
    providerLabel: knownModel?.provider || providerConfig?.provider || providerConfig?.profileName || providerConfig?.source || 'env',
    sourceLabel: sessionModelOverride ? 'session' : providerConfig?.profileName || providerConfig?.source || 'env',
    knownModel,
  };
}

export function formatModelSummary(model: ZeroModelDefinition): string {
  const caps = [
    model.capabilities.includes('reasoning') ? 'reasoning' : undefined,
    model.capabilities.includes('vision') ? 'vision' : undefined,
    model.capabilities.includes('long-context') ? 'long context' : undefined,
  ].filter(Boolean);

  return `${model.id} - ${model.displayName} (${model.provider}${caps.length ? `, ${caps.join(', ')}` : ''})`;
}

export function formatModelListLines(limit = 12): string[] {
  const models = getSelectableZeroModels();
  const visible = models.slice(0, limit);
  const lines = visible.map(formatModelSummary);

  if (models.length > visible.length) {
    lines.push(`... ${models.length - visible.length} more. Type /model to open the selector.`);
  }

  return lines;
}

export function formatModelProfileLines(): string[] {
  return listZeroModelProfiles().map(formatZeroModelProfile);
}
