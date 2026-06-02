export type ZeroModelProvider = 'openai' | 'anthropic' | 'google';

export type ZeroModelCapability =
  | 'chat'
  | 'streaming'
  | 'tool-calling'
  | 'vision'
  | 'json-mode'
  | 'reasoning'
  | 'system-prompt'
  | 'prompt-cache'
  | 'long-context';

export type ZeroReasoningEffort =
  | 'none'
  | 'minimal'
  | 'low'
  | 'medium'
  | 'high'
  | 'xhigh';

export type ZeroModelStatus = 'active' | 'preview' | 'deprecated';

export interface ZeroModelContextLimits {
  contextWindow: number;
  maxOutputTokens: number;
}

export interface ZeroModelPricingTier {
  /** Inclusive input-token ceiling for this tier; omit only on the final fallback tier. */
  upToInputTokens?: number;
  inputPerMillion: number;
  outputPerMillion: number;
  cachedInputPerMillion?: number;
  note?: string;
}

export interface ZeroModelPricing {
  currency: 'USD';
  unit: 'per_1m_tokens';
  inputPerMillion?: number;
  outputPerMillion?: number;
  cachedInputPerMillion?: number;
  tiers?: readonly ZeroModelPricingTier[];
  source: string;
  sourceLastVerified: string;
  notes?: readonly string[];
}

export interface ZeroModelDefinition {
  /** Stable Zero-facing slug. Version dots are kept when the provider markets the model that way. */
  id: string;
  displayName: string;
  /** Exact model string sent to the provider API. */
  apiModel: string;
  provider: ZeroModelProvider;
  status: ZeroModelStatus;
  /** Stable aliases only; aliases must never auto-redirect to a future model. */
  aliases: readonly string[];
  context: ZeroModelContextLimits;
  pricing: ZeroModelPricing;
  capabilities: readonly ZeroModelCapability[];
  reasoningEfforts?: readonly ZeroReasoningEffort[];
  description?: string;
}

export interface ZeroTokenUsage {
  inputTokens?: number;
  promptTokens?: number;
  cachedInputTokens?: number;
  outputTokens?: number;
  completionTokens?: number;
}

export interface ZeroModelCostBreakdown {
  modelId: string;
  provider: ZeroModelProvider;
  currency: 'USD';
  inputTokens: number;
  cachedInputTokens: number;
  outputTokens: number;
  inputCost: number;
  cachedInputCost: number;
  outputCost: number;
  totalCost: number;
  pricingTier?: ZeroModelPricingTier;
}
