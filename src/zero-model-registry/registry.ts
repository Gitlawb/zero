import type {
  ZeroModelCapability,
  ZeroModelDefinition,
  ZeroModelProvider,
  ZeroReasoningEffort,
} from './types';

const SOURCE_LAST_VERIFIED = '2026-06-02';

const PRICING_SOURCE = {
  openai: 'https://platform.openai.com/docs/pricing/',
  anthropic: 'https://docs.claude.com/en/docs/about-claude/models',
  google: 'https://ai.google.dev/gemini-api/docs/pricing',
} as const satisfies Record<ZeroModelProvider, string>;

const baseCapabilities = [
  'chat',
  'streaming',
  'tool-calling',
  'system-prompt',
] as const satisfies readonly ZeroModelCapability[];

const gpt52ReasoningEfforts = [
  'none',
  'low',
  'medium',
  'high',
  'xhigh',
] as const satisfies readonly ZeroReasoningEffort[];

const gpt5ReasoningEfforts = [
  'minimal',
  'low',
  'medium',
  'high',
] as const satisfies readonly ZeroReasoningEffort[];

const claudeReasoningEfforts = [
  'low',
  'medium',
  'high',
] as const satisfies readonly ZeroReasoningEffort[];

export const ZERO_DEFAULT_MODEL_ID = 'gpt-5.4';

export const ZERO_MODEL_REGISTRY = [
  {
    id: 'gpt-5.5',
    displayName: 'GPT-5.5',
    apiModel: 'gpt-5.5',
    provider: 'openai',
    status: 'active',
    aliases: ['openai:gpt-5.5', 'gpt-5.5-latest', 'gpt-latest'],
    context: { contextWindow: 1_050_000, maxOutputTokens: 128_000 },
    pricing: {
      currency: 'USD',
      unit: 'per_1m_tokens',
      tiers: [
        {
          upToInputTokens: 272_000,
          inputPerMillion: 5,
          cachedInputPerMillion: 0.5,
          outputPerMillion: 30,
          note: 'Standard short-context pricing.',
        },
        {
          inputPerMillion: 10,
          cachedInputPerMillion: 1,
          outputPerMillion: 45,
          note: 'Long-context pricing for prompts above 272k input tokens.',
        },
      ],
      source: PRICING_SOURCE.openai,
      sourceLastVerified: SOURCE_LAST_VERIFIED,
    },
    capabilities: [...baseCapabilities, 'vision', 'json-mode', 'reasoning', 'long-context'],
    reasoningEfforts: gpt52ReasoningEfforts,
    description: 'OpenAI newest frontier model for complex coding and professional work.',
  },
  {
    id: 'gpt-5.4',
    displayName: 'GPT-5.4',
    apiModel: 'gpt-5.4',
    provider: 'openai',
    status: 'active',
    aliases: ['openai:gpt-5.4'],
    context: { contextWindow: 1_050_000, maxOutputTokens: 128_000 },
    pricing: {
      currency: 'USD',
      unit: 'per_1m_tokens',
      tiers: [
        {
          upToInputTokens: 272_000,
          inputPerMillion: 2.5,
          cachedInputPerMillion: 0.25,
          outputPerMillion: 15,
          note: 'Standard short-context pricing.',
        },
        {
          inputPerMillion: 5,
          cachedInputPerMillion: 0.5,
          outputPerMillion: 22.5,
          note: 'Long-context pricing for prompts above 272k input tokens.',
        },
      ],
      source: PRICING_SOURCE.openai,
      sourceLastVerified: SOURCE_LAST_VERIFIED,
    },
    capabilities: [...baseCapabilities, 'vision', 'json-mode', 'reasoning', 'long-context'],
    reasoningEfforts: gpt52ReasoningEfforts,
    description: 'OpenAI balanced frontier model for Zero coding sessions.',
  },
  {
    id: 'gpt-5',
    displayName: 'GPT-5',
    apiModel: 'gpt-5',
    provider: 'openai',
    status: 'active',
    aliases: ['openai:gpt-5'],
    context: { contextWindow: 400_000, maxOutputTokens: 128_000 },
    pricing: {
      currency: 'USD',
      unit: 'per_1m_tokens',
      inputPerMillion: 1.25,
      cachedInputPerMillion: 0.125,
      outputPerMillion: 10,
      source: PRICING_SOURCE.openai,
      sourceLastVerified: SOURCE_LAST_VERIFIED,
    },
    capabilities: [...baseCapabilities, 'vision', 'json-mode', 'reasoning', 'long-context'],
    reasoningEfforts: gpt5ReasoningEfforts,
    description: 'OpenAI previous-generation reasoning model for coding and agents.',
  },
  {
    id: 'gpt-5.4-mini',
    displayName: 'GPT-5.4 mini',
    apiModel: 'gpt-5.4-mini',
    provider: 'openai',
    status: 'active',
    aliases: ['openai:gpt-5.4-mini', 'gpt-5-mini'],
    context: { contextWindow: 400_000, maxOutputTokens: 128_000 },
    pricing: {
      currency: 'USD',
      unit: 'per_1m_tokens',
      inputPerMillion: 0.75,
      cachedInputPerMillion: 0.075,
      outputPerMillion: 4.5,
      source: PRICING_SOURCE.openai,
      sourceLastVerified: SOURCE_LAST_VERIFIED,
    },
    capabilities: [...baseCapabilities, 'vision', 'json-mode', 'reasoning', 'long-context'],
    reasoningEfforts: gpt5ReasoningEfforts,
    description: 'Lower-cost OpenAI model for frequent edit and analysis loops.',
  },
  {
    id: 'gpt-5.4-nano',
    displayName: 'GPT-5.4 nano',
    apiModel: 'gpt-5.4-nano',
    provider: 'openai',
    status: 'active',
    aliases: ['openai:gpt-5.4-nano', 'gpt-5-nano'],
    context: { contextWindow: 400_000, maxOutputTokens: 128_000 },
    pricing: {
      currency: 'USD',
      unit: 'per_1m_tokens',
      inputPerMillion: 0.2,
      cachedInputPerMillion: 0.02,
      outputPerMillion: 1.25,
      source: PRICING_SOURCE.openai,
      sourceLastVerified: SOURCE_LAST_VERIFIED,
    },
    capabilities: [...baseCapabilities, 'vision', 'json-mode', 'reasoning', 'long-context'],
    reasoningEfforts: gpt5ReasoningEfforts,
    description: 'Very low-cost OpenAI model for routing, summaries, and small checks.',
  },
  {
    id: 'gpt-4.1',
    displayName: 'GPT-4.1',
    apiModel: 'gpt-4.1',
    provider: 'openai',
    status: 'active',
    aliases: ['openai:gpt-4.1'],
    context: { contextWindow: 1_047_576, maxOutputTokens: 32_768 },
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
    description: 'OpenAI long-context non-reasoning model.',
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
    id: 'claude-opus-4.8',
    displayName: 'Claude Opus 4.8',
    apiModel: 'claude-opus-4-8',
    provider: 'anthropic',
    status: 'active',
    aliases: ['anthropic:claude-opus-4.8', 'opus-4.8'],
    context: { contextWindow: 1_000_000, maxOutputTokens: 128_000 },
    pricing: {
      currency: 'USD',
      unit: 'per_1m_tokens',
      inputPerMillion: 5,
      cachedInputPerMillion: 0.5,
      outputPerMillion: 25,
      source: PRICING_SOURCE.anthropic,
      sourceLastVerified: SOURCE_LAST_VERIFIED,
    },
    capabilities: [...baseCapabilities, 'vision', 'reasoning', 'prompt-cache', 'long-context'],
    reasoningEfforts: claudeReasoningEfforts,
    description: 'Anthropic highest-capability Claude model for deep coding and planning.',
  },
  {
    id: 'claude-sonnet-4.6',
    displayName: 'Claude Sonnet 4.6',
    apiModel: 'claude-sonnet-4-6',
    provider: 'anthropic',
    status: 'active',
    aliases: ['anthropic:claude-sonnet-4.6', 'sonnet-4.6'],
    context: { contextWindow: 1_000_000, maxOutputTokens: 64_000 },
    pricing: {
      currency: 'USD',
      unit: 'per_1m_tokens',
      inputPerMillion: 3,
      cachedInputPerMillion: 0.3,
      outputPerMillion: 15,
      source: PRICING_SOURCE.anthropic,
      sourceLastVerified: SOURCE_LAST_VERIFIED,
    },
    capabilities: [...baseCapabilities, 'vision', 'reasoning', 'prompt-cache', 'long-context'],
    reasoningEfforts: claudeReasoningEfforts,
    description: 'Anthropic balanced coding model for high-quality daily agent work.',
  },
  {
    id: 'claude-haiku-4.5',
    displayName: 'Claude Haiku 4.5',
    apiModel: 'claude-haiku-4-5-20251001',
    provider: 'anthropic',
    status: 'active',
    aliases: ['anthropic:claude-haiku-4.5', 'haiku-4.5'],
    context: { contextWindow: 200_000, maxOutputTokens: 64_000 },
    pricing: {
      currency: 'USD',
      unit: 'per_1m_tokens',
      inputPerMillion: 1,
      cachedInputPerMillion: 0.1,
      outputPerMillion: 5,
      source: PRICING_SOURCE.anthropic,
      sourceLastVerified: SOURCE_LAST_VERIFIED,
    },
    capabilities: [...baseCapabilities, 'vision', 'reasoning', 'prompt-cache'],
    reasoningEfforts: claudeReasoningEfforts,
    description: 'Anthropic fast model for lightweight coding support and summaries.',
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
    },
    capabilities: [...baseCapabilities, 'vision', 'reasoning', 'prompt-cache'],
    reasoningEfforts: claudeReasoningEfforts,
    description: 'Anthropic stable Sonnet model for provider compatibility.',
  },
  {
    id: 'gemini-3.1-pro-preview',
    displayName: 'Gemini 3.1 Pro Preview',
    apiModel: 'gemini-3.1-pro-preview',
    provider: 'google',
    status: 'preview',
    aliases: ['google:gemini-3.1-pro-preview', 'gemini-3.1-pro', 'gemini-pro'],
    context: { contextWindow: 1_048_576, maxOutputTokens: 65_536 },
    pricing: {
      currency: 'USD',
      unit: 'per_1m_tokens',
      tiers: [
        {
          upToInputTokens: 200_000,
          inputPerMillion: 2,
          outputPerMillion: 12,
          note: 'Prompts up to 200k tokens.',
        },
        {
          inputPerMillion: 4,
          outputPerMillion: 18,
          note: 'Prompts above 200k tokens.',
        },
      ],
      source: PRICING_SOURCE.google,
      sourceLastVerified: SOURCE_LAST_VERIFIED,
    },
    capabilities: [...baseCapabilities, 'vision', 'json-mode', 'reasoning', 'long-context'],
    reasoningEfforts: claudeReasoningEfforts,
    description: 'Google preview model for high-capability multimodal agent work.',
  },
  {
    id: 'gemini-3-flash-preview',
    displayName: 'Gemini 3 Flash Preview',
    apiModel: 'gemini-3-flash-preview',
    provider: 'google',
    status: 'preview',
    aliases: ['google:gemini-3-flash-preview', 'gemini-3-flash', 'gemini-flash-preview'],
    context: { contextWindow: 1_048_576, maxOutputTokens: 65_536 },
    pricing: {
      currency: 'USD',
      unit: 'per_1m_tokens',
      inputPerMillion: 0.5,
      cachedInputPerMillion: 0.05,
      outputPerMillion: 3,
      source: PRICING_SOURCE.google,
      sourceLastVerified: SOURCE_LAST_VERIFIED,
    },
    capabilities: [...baseCapabilities, 'vision', 'json-mode', 'reasoning', 'long-context'],
    reasoningEfforts: claudeReasoningEfforts,
    description: 'Google Flash preview for fast multimodal coding sessions.',
  },
  {
    id: 'gemini-3.1-flash-lite',
    displayName: 'Gemini 3.1 Flash-Lite',
    apiModel: 'gemini-3.1-flash-lite',
    provider: 'google',
    status: 'active',
    aliases: ['google:gemini-3.1-flash-lite', 'gemini-3.1-lite'],
    context: { contextWindow: 1_048_576, maxOutputTokens: 65_536 },
    pricing: {
      currency: 'USD',
      unit: 'per_1m_tokens',
      inputPerMillion: 0.25,
      outputPerMillion: 1.5,
      source: PRICING_SOURCE.google,
      sourceLastVerified: SOURCE_LAST_VERIFIED,
    },
    capabilities: [...baseCapabilities, 'vision', 'json-mode', 'reasoning', 'long-context'],
    reasoningEfforts: ['minimal', 'low', 'medium', 'high'],
    description: 'Google cost-efficient Gemini 3.1 workhorse for high-volume tasks.',
  },
  {
    id: 'gemini-2.5-pro',
    displayName: 'Gemini 2.5 Pro',
    apiModel: 'gemini-2.5-pro',
    provider: 'google',
    status: 'active',
    aliases: ['google:gemini-2.5-pro', 'gemini-2.5-pro-latest'],
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
    reasoningEfforts: claudeReasoningEfforts,
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
    reasoningEfforts: claudeReasoningEfforts,
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
    reasoningEfforts: claudeReasoningEfforts,
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
  return modelsById.get(normalizeModelKey(modelId));
}

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
