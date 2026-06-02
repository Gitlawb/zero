import type {
  ZeroModelCapability,
  ZeroModelDefinition,
  ZeroModelProvider,
  ZeroReasoningEffort,
} from './types';

const SOURCE_LAST_VERIFIED = '2026-06-02';

const PRICING_SOURCE = {
  openai: 'https://platform.openai.com/docs/pricing/',
  anthropic: 'https://docs.claude.com/en/docs/about-claude/pricing',
  google: 'https://ai.google.dev/gemini-api/docs/pricing',
} as const satisfies Record<ZeroModelProvider, string>;

const baseCapabilities = [
  'chat',
  'streaming',
  'tool-calling',
  'system-prompt',
] as const satisfies readonly ZeroModelCapability[];

const standardReasoningEfforts = [
  'low',
  'medium',
  'high',
] as const satisfies readonly ZeroReasoningEffort[];

export const ZERO_DEFAULT_MODEL_ID = 'gpt-4.1';

export const ZERO_MODEL_REGISTRY = [
  {
    id: 'gpt-4.1',
    displayName: 'GPT-4.1',
    apiModel: 'gpt-4.1',
    provider: 'openai',
    status: 'active',
    aliases: ['openai:gpt-4.1'],
    context: { contextWindow: 1_047_576, maxOutputTokens: 16_384 },
    pricing: {
      currency: 'USD',
      unit: 'per_1m_tokens',
      inputPerMillion: 2,
      cachedInputPerMillion: 0.5,
      outputPerMillion: 8,
      source: PRICING_SOURCE.openai,
      sourceLastVerified: SOURCE_LAST_VERIFIED,
    },
    capabilities: [...baseCapabilities, 'vision', 'json-mode', 'long-context'],
    description: 'OpenAI stable long-context model for general coding sessions.',
  },
  {
    id: 'gpt-4.1-mini',
    displayName: 'GPT-4.1 mini',
    apiModel: 'gpt-4.1-mini',
    provider: 'openai',
    status: 'active',
    aliases: ['openai:gpt-4.1-mini'],
    context: { contextWindow: 1_047_576, maxOutputTokens: 32_768 },
    pricing: {
      currency: 'USD',
      unit: 'per_1m_tokens',
      inputPerMillion: 0.4,
      cachedInputPerMillion: 0.1,
      outputPerMillion: 1.6,
      source: PRICING_SOURCE.openai,
      sourceLastVerified: SOURCE_LAST_VERIFIED,
    },
    capabilities: [...baseCapabilities, 'vision', 'json-mode', 'long-context'],
    description: 'OpenAI lower-cost long-context model for frequent edit loops.',
  },
  {
    id: 'gpt-4.1-nano',
    displayName: 'GPT-4.1 nano',
    apiModel: 'gpt-4.1-nano',
    provider: 'openai',
    status: 'active',
    aliases: ['openai:gpt-4.1-nano'],
    context: { contextWindow: 1_047_576, maxOutputTokens: 32_768 },
    pricing: {
      currency: 'USD',
      unit: 'per_1m_tokens',
      inputPerMillion: 0.1,
      cachedInputPerMillion: 0.025,
      outputPerMillion: 0.4,
      source: PRICING_SOURCE.openai,
      sourceLastVerified: SOURCE_LAST_VERIFIED,
    },
    capabilities: [...baseCapabilities, 'vision', 'json-mode', 'long-context'],
    description: 'OpenAI smallest GPT-4.1 model for routing, summaries, and light checks.',
  },
  {
    id: 'gpt-4o',
    displayName: 'GPT-4o',
    apiModel: 'gpt-4o',
    provider: 'openai',
    status: 'active',
    aliases: ['openai:gpt-4o'],
    context: { contextWindow: 128_000, maxOutputTokens: 16_384 },
    pricing: {
      currency: 'USD',
      unit: 'per_1m_tokens',
      inputPerMillion: 2.5,
      cachedInputPerMillion: 1.25,
      outputPerMillion: 10,
      source: PRICING_SOURCE.openai,
      sourceLastVerified: SOURCE_LAST_VERIFIED,
    },
    capabilities: [...baseCapabilities, 'vision', 'json-mode'],
    description: 'OpenAI multimodal model kept for compatibility with the current Zero config.',
  },
  {
    id: 'gpt-4o-mini',
    displayName: 'GPT-4o mini',
    apiModel: 'gpt-4o-mini',
    provider: 'openai',
    status: 'active',
    aliases: ['openai:gpt-4o-mini'],
    context: { contextWindow: 128_000, maxOutputTokens: 16_384 },
    pricing: {
      currency: 'USD',
      unit: 'per_1m_tokens',
      inputPerMillion: 0.15,
      cachedInputPerMillion: 0.075,
      outputPerMillion: 0.6,
      source: PRICING_SOURCE.openai,
      sourceLastVerified: SOURCE_LAST_VERIFIED,
    },
    capabilities: [...baseCapabilities, 'vision', 'json-mode'],
    description: 'OpenAI low-cost multimodal model for lightweight sessions.',
  },
  {
    id: 'gpt-4-turbo',
    displayName: 'GPT-4 Turbo',
    apiModel: 'gpt-4-turbo',
    provider: 'openai',
    status: 'deprecated',
    aliases: ['openai:gpt-4-turbo'],
    context: { contextWindow: 128_000, maxOutputTokens: 4_096 },
    pricing: {
      currency: 'USD',
      unit: 'per_1m_tokens',
      inputPerMillion: 10,
      outputPerMillion: 30,
      source: PRICING_SOURCE.openai,
      sourceLastVerified: SOURCE_LAST_VERIFIED,
    },
    capabilities: [...baseCapabilities, 'vision', 'json-mode'],
    description: 'Deprecated OpenAI model retained for config migration and history display.',
  },
  {
    id: 'claude-opus-4.1',
    displayName: 'Claude Opus 4.1',
    apiModel: 'claude-opus-4-1-20250805',
    provider: 'anthropic',
    status: 'active',
    aliases: ['anthropic:claude-opus-4.1', 'opus-4.1'],
    context: { contextWindow: 200_000, maxOutputTokens: 32_000 },
    pricing: {
      currency: 'USD',
      unit: 'per_1m_tokens',
      inputPerMillion: 15,
      cachedInputPerMillion: 1.5,
      outputPerMillion: 75,
      source: PRICING_SOURCE.anthropic,
      sourceLastVerified: SOURCE_LAST_VERIFIED,
      notes: ['cachedInputPerMillion models Claude cache hit and refresh pricing.'],
    },
    capabilities: [...baseCapabilities, 'vision', 'reasoning', 'prompt-cache'],
    reasoningEfforts: standardReasoningEfforts,
    description: 'Anthropic highest-capability Claude model for deep coding and planning.',
  },
  {
    id: 'claude-opus-4',
    displayName: 'Claude Opus 4',
    apiModel: 'claude-opus-4-20250514',
    provider: 'anthropic',
    status: 'active',
    aliases: ['anthropic:claude-opus-4', 'opus-4'],
    context: { contextWindow: 200_000, maxOutputTokens: 32_000 },
    pricing: {
      currency: 'USD',
      unit: 'per_1m_tokens',
      inputPerMillion: 15,
      cachedInputPerMillion: 1.5,
      outputPerMillion: 75,
      source: PRICING_SOURCE.anthropic,
      sourceLastVerified: SOURCE_LAST_VERIFIED,
      notes: ['cachedInputPerMillion models Claude cache hit and refresh pricing.'],
    },
    capabilities: [...baseCapabilities, 'vision', 'reasoning', 'prompt-cache'],
    reasoningEfforts: standardReasoningEfforts,
    description: 'Anthropic Opus model for complex coding tasks.',
  },
  {
    id: 'claude-sonnet-4.5',
    displayName: 'Claude Sonnet 4.5',
    apiModel: 'claude-sonnet-4-5-20250929',
    provider: 'anthropic',
    status: 'active',
    aliases: ['anthropic:claude-sonnet-4.5', 'sonnet-4.5'],
    context: { contextWindow: 200_000, maxOutputTokens: 64_000 },
    pricing: {
      currency: 'USD',
      unit: 'per_1m_tokens',
      inputPerMillion: 3,
      cachedInputPerMillion: 0.3,
      outputPerMillion: 15,
      source: PRICING_SOURCE.anthropic,
      sourceLastVerified: SOURCE_LAST_VERIFIED,
      notes: ['cachedInputPerMillion models Claude cache hit and refresh pricing.'],
    },
    capabilities: [...baseCapabilities, 'vision', 'reasoning', 'prompt-cache'],
    reasoningEfforts: standardReasoningEfforts,
    description: 'Anthropic balanced coding model for high-quality daily agent work.',
  },
  {
    id: 'claude-sonnet-4',
    displayName: 'Claude Sonnet 4',
    apiModel: 'claude-sonnet-4-20250514',
    provider: 'anthropic',
    status: 'active',
    aliases: ['anthropic:claude-sonnet-4', 'sonnet-4'],
    context: { contextWindow: 200_000, maxOutputTokens: 64_000 },
    pricing: {
      currency: 'USD',
      unit: 'per_1m_tokens',
      inputPerMillion: 3,
      cachedInputPerMillion: 0.3,
      outputPerMillion: 15,
      source: PRICING_SOURCE.anthropic,
      sourceLastVerified: SOURCE_LAST_VERIFIED,
      notes: ['cachedInputPerMillion models Claude cache hit and refresh pricing.'],
    },
    capabilities: [...baseCapabilities, 'vision', 'reasoning', 'prompt-cache'],
    reasoningEfforts: standardReasoningEfforts,
    description: 'Anthropic stable Sonnet model for provider compatibility.',
  },
  {
    id: 'claude-haiku-4.5',
    displayName: 'Claude Haiku 4.5',
    apiModel: 'claude-haiku-4-5-20251001',
    provider: 'anthropic',
    status: 'active',
    aliases: ['anthropic:claude-haiku-4.5', 'haiku-4.5'],
    context: { contextWindow: 200_000, maxOutputTokens: 8_192 },
    pricing: {
      currency: 'USD',
      unit: 'per_1m_tokens',
      inputPerMillion: 1,
      cachedInputPerMillion: 0.1,
      outputPerMillion: 5,
      source: PRICING_SOURCE.anthropic,
      sourceLastVerified: SOURCE_LAST_VERIFIED,
      notes: ['cachedInputPerMillion models Claude cache hit and refresh pricing.'],
    },
    capabilities: [...baseCapabilities, 'vision', 'reasoning', 'prompt-cache'],
    reasoningEfforts: standardReasoningEfforts,
    description: 'Anthropic fast model for lightweight coding support and summaries.',
  },
  {
    id: 'claude-haiku-3.5',
    displayName: 'Claude Haiku 3.5',
    apiModel: 'claude-3-5-haiku-20241022',
    provider: 'anthropic',
    status: 'active',
    aliases: ['anthropic:claude-haiku-3.5', 'haiku-3.5'],
    context: { contextWindow: 200_000, maxOutputTokens: 8_192 },
    pricing: {
      currency: 'USD',
      unit: 'per_1m_tokens',
      inputPerMillion: 0.8,
      cachedInputPerMillion: 0.08,
      outputPerMillion: 4,
      source: PRICING_SOURCE.anthropic,
      sourceLastVerified: SOURCE_LAST_VERIFIED,
      notes: ['cachedInputPerMillion models Claude cache hit and refresh pricing.'],
    },
    capabilities: [...baseCapabilities, 'vision', 'prompt-cache'],
    description: 'Anthropic low-latency Haiku model for economical coding support.',
  },
  {
    id: 'gemini-2.5-pro',
    displayName: 'Gemini 2.5 Pro',
    apiModel: 'gemini-2.5-pro',
    provider: 'google',
    status: 'active',
    aliases: ['google:gemini-2.5-pro', 'gemini-pro'],
    context: { contextWindow: 1_048_576, maxOutputTokens: 65_536 },
    pricing: {
      currency: 'USD',
      unit: 'per_1m_tokens',
      tiers: [
        {
          upToInputTokens: 200_000,
          inputPerMillion: 1.25,
          outputPerMillion: 10,
          note: 'Prompts up to 200k tokens.',
        },
        {
          inputPerMillion: 2.5,
          outputPerMillion: 15,
          note: 'Prompts above 200k tokens.',
        },
      ],
      source: PRICING_SOURCE.google,
      sourceLastVerified: SOURCE_LAST_VERIFIED,
    },
    capabilities: [...baseCapabilities, 'vision', 'json-mode', 'reasoning', 'long-context'],
    reasoningEfforts: standardReasoningEfforts,
    description: 'Google general-purpose Pro model with tiered long-context pricing.',
  },
  {
    id: 'gemini-2.5-flash',
    displayName: 'Gemini 2.5 Flash',
    apiModel: 'gemini-2.5-flash',
    provider: 'google',
    status: 'active',
    aliases: ['google:gemini-2.5-flash', 'gemini-flash'],
    context: { contextWindow: 1_048_576, maxOutputTokens: 65_536 },
    pricing: {
      currency: 'USD',
      unit: 'per_1m_tokens',
      inputPerMillion: 0.3,
      cachedInputPerMillion: 0.03,
      outputPerMillion: 2.5,
      source: PRICING_SOURCE.google,
      sourceLastVerified: SOURCE_LAST_VERIFIED,
    },
    capabilities: [...baseCapabilities, 'vision', 'json-mode', 'reasoning', 'long-context'],
    reasoningEfforts: standardReasoningEfforts,
    description: 'Google Flash model for low-latency coding interactions.',
  },
  {
    id: 'gemini-2.5-flash-lite',
    displayName: 'Gemini 2.5 Flash-Lite',
    apiModel: 'gemini-2.5-flash-lite',
    provider: 'google',
    status: 'active',
    aliases: ['google:gemini-2.5-flash-lite', 'gemini-flash-lite'],
    context: { contextWindow: 1_048_576, maxOutputTokens: 65_536 },
    pricing: {
      currency: 'USD',
      unit: 'per_1m_tokens',
      inputPerMillion: 0.1,
      cachedInputPerMillion: 0.025,
      outputPerMillion: 0.4,
      source: PRICING_SOURCE.google,
      sourceLastVerified: SOURCE_LAST_VERIFIED,
    },
    capabilities: [...baseCapabilities, 'vision', 'json-mode', 'reasoning', 'long-context'],
    reasoningEfforts: standardReasoningEfforts,
    description: 'Google low-cost Flash model for background routing and summaries.',
  },
] as const satisfies readonly ZeroModelDefinition[];

export type ZeroModelId = (typeof ZERO_MODEL_REGISTRY)[number]['id'];

const modelsById = new Map<string, ZeroModelDefinition>();
const aliasesByName = new Map<string, string>();

for (const model of ZERO_MODEL_REGISTRY) {
  const idKey = normalizeModelKey(model.id);
  if (modelsById.has(idKey)) {
    throw new Error(`Duplicate Zero model id: ${model.id}`);
  }
  modelsById.set(idKey, model);

  for (const alias of model.aliases) {
    const aliasKey = normalizeModelKey(alias);
    if (aliasesByName.has(aliasKey) || modelsById.has(aliasKey)) {
      throw new Error(`Duplicate Zero model alias: ${alias}`);
    }
    aliasesByName.set(aliasKey, model.id);
  }
}

export function listZeroModels(
  options: { includeDeprecated?: boolean } = {}
): ZeroModelDefinition[] {
  return ZERO_MODEL_REGISTRY.filter(
    (model) => options.includeDeprecated || model.status !== 'deprecated'
  );
}

export function resolveZeroModelId(modelOrAlias: string): string | undefined {
  const key = normalizeModelKey(modelOrAlias);
  if (modelsById.has(key)) return modelsById.get(key)?.id;
  return aliasesByName.get(key);
}

export function getZeroModel(modelOrAlias: string): ZeroModelDefinition | undefined {
  const modelId = resolveZeroModelId(modelOrAlias);
  if (!modelId) return undefined;
  return modelsById.get(modelId);
}

/** Returns a model or throws a caller-facing error for invalid config/CLI input. */
export function requireZeroModel(modelOrAlias: string): ZeroModelDefinition {
  const model = getZeroModel(modelOrAlias);
  if (!model) {
    throw new Error(`Unknown Zero model: ${modelOrAlias}`);
  }
  return model;
}

export function isKnownZeroModel(modelOrAlias: string): boolean {
  return getZeroModel(modelOrAlias) !== undefined;
}

export function listZeroModelsByProvider(provider: ZeroModelProvider): ZeroModelDefinition[] {
  return listZeroModels().filter((model) => model.provider === provider);
}

export function listZeroModelsByCapability(
  capability: ZeroModelCapability
): ZeroModelDefinition[] {
  return listZeroModels().filter((model) =>
    model.capabilities.includes(capability)
  );
}

export function zeroModelSupportsCapability(
  modelOrAlias: string,
  capability: ZeroModelCapability
): boolean {
  return getZeroModel(modelOrAlias)?.capabilities.includes(capability) ?? false;
}

export function getZeroReasoningEfforts(modelOrAlias: string): readonly ZeroReasoningEffort[] {
  return getZeroModel(modelOrAlias)?.reasoningEfforts ?? [];
}

/** Validates that a resolved model belongs to the provider selected by the active profile. */
export function assertZeroModelProvider(
  modelOrAlias: string,
  provider: ZeroModelProvider
): ZeroModelDefinition {
  const model = requireZeroModel(modelOrAlias);
  if (model.provider !== provider) {
    throw new Error(
      `Zero model ${model.id} belongs to ${model.provider}, not ${provider}`
    );
  }
  return model;
}

function normalizeModelKey(value: string): string {
  return value.trim().toLowerCase();
}
