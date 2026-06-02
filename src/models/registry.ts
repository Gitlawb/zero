import type {
  ModelCapability,
  ModelDefinition,
  ModelProvider,
  ReasoningEffort,
} from './types';

const VERIFIED_AT = '2026-06-02';

const OPENAI_PRICING = 'https://openai.com/api/pricing/';
const ANTHROPIC_PRICING = 'https://platform.claude.com/docs/en/about-claude/pricing';
const GEMINI_PRICING = 'https://ai.google.dev/gemini-api/docs/pricing';

export const DEFAULT_MODEL_ID = 'claude-sonnet-4-6';

export const MODELS: readonly ModelDefinition[] = [
  {
    id: 'claude-opus-4-8',
    displayName: 'Claude Opus 4.8',
    provider: 'anthropic',
    apiModel: 'claude-opus-4-8',
    aliases: ['opus-4.8', 'claude-opus-latest'],
    context: { contextWindowTokens: 200_000, maxOutputTokens: 64_000 },
    capabilities: ['text', 'tools', 'vision', 'reasoning', 'promptCaching'],
    supportedReasoningEfforts: ['low', 'medium', 'high'],
    defaultReasoningEffort: 'medium',
    pricing: {
      inputPerMillionUsd: 5,
      outputPerMillionUsd: 25,
      cacheReadInputPerMillionUsd: 0.5,
      cacheWriteInputPerMillionUsd: 6.25,
      source: ANTHROPIC_PRICING,
      lastVerified: VERIFIED_AT,
    },
  },
  {
    id: 'claude-opus-4-7',
    displayName: 'Claude Opus 4.7',
    provider: 'anthropic',
    apiModel: 'claude-opus-4-7',
    aliases: ['opus-4.7', 'claude-opus-4.7'],
    context: { contextWindowTokens: 200_000, maxOutputTokens: 64_000 },
    capabilities: ['text', 'tools', 'vision', 'reasoning', 'promptCaching'],
    supportedReasoningEfforts: ['low', 'medium', 'high'],
    defaultReasoningEffort: 'medium',
    pricing: {
      inputPerMillionUsd: 5,
      outputPerMillionUsd: 25,
      cacheReadInputPerMillionUsd: 0.5,
      cacheWriteInputPerMillionUsd: 6.25,
      source: ANTHROPIC_PRICING,
      lastVerified: VERIFIED_AT,
    },
  },
  {
    id: 'claude-opus-4-6',
    displayName: 'Claude Opus 4.6',
    provider: 'anthropic',
    apiModel: 'claude-opus-4-6',
    aliases: ['opus-4.6', 'claude-opus-4.6'],
    context: { contextWindowTokens: 200_000, maxOutputTokens: 64_000 },
    capabilities: ['text', 'tools', 'vision', 'reasoning', 'promptCaching'],
    supportedReasoningEfforts: ['low', 'medium', 'high'],
    defaultReasoningEffort: 'medium',
    pricing: {
      inputPerMillionUsd: 5,
      outputPerMillionUsd: 25,
      cacheReadInputPerMillionUsd: 0.5,
      cacheWriteInputPerMillionUsd: 6.25,
      source: ANTHROPIC_PRICING,
      lastVerified: VERIFIED_AT,
    },
  },
  {
    id: 'claude-sonnet-4-6',
    displayName: 'Claude Sonnet 4.6',
    provider: 'anthropic',
    apiModel: 'claude-sonnet-4-6',
    aliases: ['sonnet-4.6', 'claude-sonnet-4.6', 'claude-sonnet'],
    context: { contextWindowTokens: 200_000, maxOutputTokens: 64_000 },
    capabilities: ['text', 'tools', 'vision', 'reasoning', 'promptCaching'],
    supportedReasoningEfforts: ['low', 'medium', 'high'],
    defaultReasoningEffort: 'medium',
    pricing: {
      inputPerMillionUsd: 3,
      outputPerMillionUsd: 15,
      cacheReadInputPerMillionUsd: 0.3,
      cacheWriteInputPerMillionUsd: 3.75,
      source: ANTHROPIC_PRICING,
      lastVerified: VERIFIED_AT,
    },
  },
  {
    id: 'claude-sonnet-4-5',
    displayName: 'Claude Sonnet 4.5',
    provider: 'anthropic',
    apiModel: 'claude-sonnet-4-5',
    aliases: ['sonnet-4.5', 'claude-sonnet-4.5'],
    context: { contextWindowTokens: 200_000, maxOutputTokens: 64_000 },
    capabilities: ['text', 'tools', 'vision', 'reasoning', 'promptCaching'],
    supportedReasoningEfforts: ['low', 'medium', 'high'],
    defaultReasoningEffort: 'medium',
    pricing: {
      inputPerMillionUsd: 3,
      outputPerMillionUsd: 15,
      cacheReadInputPerMillionUsd: 0.3,
      cacheWriteInputPerMillionUsd: 3.75,
      source: ANTHROPIC_PRICING,
      lastVerified: VERIFIED_AT,
    },
  },
  {
    id: 'claude-haiku-4-5',
    displayName: 'Claude Haiku 4.5',
    provider: 'anthropic',
    apiModel: 'claude-haiku-4-5',
    aliases: ['haiku-4.5', 'claude-haiku'],
    context: { contextWindowTokens: 200_000, maxOutputTokens: 32_000 },
    capabilities: ['text', 'tools', 'vision', 'promptCaching'],
    pricing: {
      inputPerMillionUsd: 1,
      outputPerMillionUsd: 5,
      cacheReadInputPerMillionUsd: 0.1,
      cacheWriteInputPerMillionUsd: 1.25,
      source: ANTHROPIC_PRICING,
      lastVerified: VERIFIED_AT,
    },
  },
  {
    id: 'gpt-5.5',
    displayName: 'GPT-5.5',
    provider: 'openai',
    apiModel: 'gpt-5.5',
    aliases: ['gpt-5.5-latest'],
    context: { contextWindowTokens: 270_000, maxOutputTokens: 64_000 },
    capabilities: ['text', 'tools', 'vision', 'reasoning', 'promptCaching'],
    supportedReasoningEfforts: ['low', 'medium', 'high'],
    defaultReasoningEffort: 'medium',
    pricing: {
      inputPerMillionUsd: 5,
      outputPerMillionUsd: 30,
      cacheReadInputPerMillionUsd: 0.5,
      source: OPENAI_PRICING,
      lastVerified: VERIFIED_AT,
    },
  },
  {
    id: 'gpt-5.4',
    displayName: 'GPT-5.4',
    provider: 'openai',
    apiModel: 'gpt-5.4',
    aliases: ['gpt-5.4-latest'],
    context: { contextWindowTokens: 270_000, maxOutputTokens: 64_000 },
    capabilities: ['text', 'tools', 'vision', 'reasoning', 'promptCaching'],
    supportedReasoningEfforts: ['low', 'medium', 'high'],
    defaultReasoningEffort: 'medium',
    pricing: {
      inputPerMillionUsd: 2.5,
      outputPerMillionUsd: 15,
      cacheReadInputPerMillionUsd: 0.25,
      source: OPENAI_PRICING,
      lastVerified: VERIFIED_AT,
    },
  },
  {
    id: 'gpt-5.4-mini',
    displayName: 'GPT-5.4 Mini',
    provider: 'openai',
    apiModel: 'gpt-5.4-mini',
    aliases: ['gpt-5-mini'],
    context: { contextWindowTokens: 270_000, maxOutputTokens: 64_000 },
    capabilities: ['text', 'tools', 'vision', 'reasoning', 'promptCaching'],
    supportedReasoningEfforts: ['low', 'medium', 'high'],
    defaultReasoningEffort: 'medium',
    pricing: {
      inputPerMillionUsd: 0.75,
      outputPerMillionUsd: 4.5,
      cacheReadInputPerMillionUsd: 0.075,
      source: OPENAI_PRICING,
      lastVerified: VERIFIED_AT,
    },
  },
  {
    id: 'gpt-4o',
    displayName: 'GPT-4o',
    provider: 'openai',
    apiModel: 'gpt-4o',
    aliases: ['gpt-4o-latest'],
    context: { contextWindowTokens: 128_000, maxOutputTokens: 16_384 },
    capabilities: ['text', 'tools', 'vision'],
    pricing: {
      inputPerMillionUsd: 2.5,
      outputPerMillionUsd: 10,
      cacheReadInputPerMillionUsd: 1.25,
      source: OPENAI_PRICING,
      lastVerified: VERIFIED_AT,
    },
  },
  {
    id: 'gpt-4-turbo',
    displayName: 'GPT-4 Turbo',
    provider: 'openai',
    apiModel: 'gpt-4-turbo',
    aliases: ['gpt-4-turbo-preview'],
    context: { contextWindowTokens: 128_000, maxOutputTokens: 4_096 },
    capabilities: ['text', 'tools', 'vision'],
    deprecated: true,
    pricing: {
      inputPerMillionUsd: 10,
      outputPerMillionUsd: 30,
      source: OPENAI_PRICING,
      lastVerified: VERIFIED_AT,
    },
  },
  {
    id: 'gemini-3.5-flash',
    displayName: 'Gemini 3.5 Flash',
    provider: 'google',
    apiModel: 'gemini-3.5-flash',
    aliases: ['gemini-flash-latest'],
    context: { contextWindowTokens: 1_000_000, maxOutputTokens: 65_536 },
    capabilities: ['text', 'tools', 'vision', 'reasoning', 'promptCaching'],
    supportedReasoningEfforts: ['low', 'medium', 'high'],
    defaultReasoningEffort: 'medium',
    pricing: {
      inputPerMillionUsd: 1.5,
      outputPerMillionUsd: 9,
      cacheReadInputPerMillionUsd: 0.15,
      source: GEMINI_PRICING,
      lastVerified: VERIFIED_AT,
    },
  },
  {
    id: 'gemini-3.1-flash-lite',
    displayName: 'Gemini 3.1 Flash-Lite',
    provider: 'google',
    apiModel: 'gemini-3.1-flash-lite',
    aliases: ['gemini-flash-lite-latest'],
    context: { contextWindowTokens: 1_000_000, maxOutputTokens: 65_536 },
    capabilities: ['text', 'tools', 'vision', 'reasoning', 'promptCaching'],
    supportedReasoningEfforts: ['low', 'medium', 'high'],
    defaultReasoningEffort: 'low',
    pricing: {
      inputPerMillionUsd: 0.25,
      outputPerMillionUsd: 1.5,
      cacheReadInputPerMillionUsd: 0.025,
      source: GEMINI_PRICING,
      lastVerified: VERIFIED_AT,
    },
  },
  {
    id: 'gemini-2.5-pro',
    displayName: 'Gemini 2.5 Pro',
    provider: 'google',
    apiModel: 'gemini-2.5-pro',
    aliases: ['gemini-pro', 'gemini-pro-2.5'],
    context: { contextWindowTokens: 1_048_576, maxOutputTokens: 65_536 },
    capabilities: ['text', 'tools', 'vision', 'reasoning', 'promptCaching'],
    supportedReasoningEfforts: ['low', 'medium', 'high'],
    defaultReasoningEffort: 'medium',
    pricing: {
      inputPerMillionUsd: 1.25,
      outputPerMillionUsd: 10,
      cacheReadInputPerMillionUsd: 0.125,
      tiers: [
        {
          upToInputTokens: 200_000,
          inputPerMillionUsd: 1.25,
          outputPerMillionUsd: 10,
          cacheReadInputPerMillionUsd: 0.125,
        },
        {
          inputPerMillionUsd: 2.5,
          outputPerMillionUsd: 15,
          cacheReadInputPerMillionUsd: 0.25,
        },
      ],
      source: GEMINI_PRICING,
      lastVerified: VERIFIED_AT,
    },
  },
  {
    id: 'gemini-2.5-flash',
    displayName: 'Gemini 2.5 Flash',
    provider: 'google',
    apiModel: 'gemini-2.5-flash',
    aliases: ['gemini-flash', 'gemini-flash-2.5'],
    context: { contextWindowTokens: 1_048_576, maxOutputTokens: 65_536 },
    capabilities: ['text', 'tools', 'vision', 'reasoning', 'promptCaching'],
    supportedReasoningEfforts: ['low', 'medium', 'high'],
    defaultReasoningEffort: 'medium',
    pricing: {
      inputPerMillionUsd: 0.3,
      outputPerMillionUsd: 2.5,
      cacheReadInputPerMillionUsd: 0.03,
      source: GEMINI_PRICING,
      lastVerified: VERIFIED_AT,
    },
  },
  {
    id: 'gemini-2.5-flash-lite',
    displayName: 'Gemini 2.5 Flash-Lite',
    provider: 'google',
    apiModel: 'gemini-2.5-flash-lite',
    aliases: ['gemini-flash-lite', 'gemini-flash-lite-2.5'],
    context: { contextWindowTokens: 1_048_576, maxOutputTokens: 65_536 },
    capabilities: ['text', 'tools', 'vision', 'reasoning', 'promptCaching'],
    supportedReasoningEfforts: ['low', 'medium', 'high'],
    defaultReasoningEffort: 'low',
    pricing: {
      inputPerMillionUsd: 0.1,
      outputPerMillionUsd: 0.4,
      cacheReadInputPerMillionUsd: 0.01,
      source: GEMINI_PRICING,
      lastVerified: VERIFIED_AT,
    },
  },
];

const modelById = new Map(MODELS.map((model) => [model.id, model]));
const aliasToModelId = new Map<string, string>();

for (const model of MODELS) {
  aliasToModelId.set(model.id.toLowerCase(), model.id);
  for (const alias of model.aliases) {
    aliasToModelId.set(alias.toLowerCase(), model.id);
  }
}

export interface ListModelsOptions {
  provider?: ModelProvider;
  capability?: ModelCapability;
  includeDeprecated?: boolean;
}

export function listModels(options: ListModelsOptions = {}): ModelDefinition[] {
  return MODELS.filter((model) => {
    if (!options.includeDeprecated && model.deprecated) return false;
    if (options.provider && model.provider !== options.provider) return false;
    if (options.capability && !model.capabilities.includes(options.capability)) return false;
    return true;
  }).map(cloneModel);
}

export function resolveModelId(modelIdOrAlias: string): string | undefined {
  const normalized = modelIdOrAlias.trim().toLowerCase();
  return aliasToModelId.get(normalized);
}

export function getModel(modelIdOrAlias: string): ModelDefinition | undefined {
  const resolved = resolveModelId(modelIdOrAlias);
  if (!resolved) return undefined;
  const model = modelById.get(resolved);
  return model ? cloneModel(model) : undefined;
}

export function requireModel(modelIdOrAlias: string): ModelDefinition {
  const model = getModel(modelIdOrAlias);
  if (!model) {
    throw new Error(`Unknown model: ${modelIdOrAlias}`);
  }
  return model;
}

export function isKnownModel(modelIdOrAlias: string): boolean {
  return resolveModelId(modelIdOrAlias) !== undefined;
}

export function listModelsByProvider(provider: ModelProvider, includeDeprecated = false): ModelDefinition[] {
  return listModels({ provider, includeDeprecated });
}

export function listModelsByCapability(capability: ModelCapability): ModelDefinition[] {
  return listModels({ capability });
}

export function modelSupportsCapability(modelIdOrAlias: string, capability: ModelCapability): boolean {
  return requireModel(modelIdOrAlias).capabilities.includes(capability);
}

export function getReasoningEfforts(modelIdOrAlias: string): ReasoningEffort[] {
  return [...(requireModel(modelIdOrAlias).supportedReasoningEfforts ?? [])];
}

export function assertModelProvider(modelIdOrAlias: string, provider: ModelProvider): ModelDefinition {
  const model = requireModel(modelIdOrAlias);
  if (model.provider !== provider) {
    throw new Error(`Model ${model.id} is provided by ${model.provider}, not ${provider}`);
  }
  return model;
}

function cloneModel(model: ModelDefinition): ModelDefinition {
  return {
    ...model,
    aliases: [...model.aliases],
    capabilities: [...model.capabilities],
    supportedReasoningEfforts: model.supportedReasoningEfforts
      ? [...model.supportedReasoningEfforts]
      : undefined,
    pricing: {
      ...model.pricing,
      tiers: model.pricing.tiers?.map((tier) => ({ ...tier })),
    },
  };
}
