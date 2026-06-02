export type ModelProvider = 'openai' | 'anthropic' | 'google';

export type ReasoningEffort = 'low' | 'medium' | 'high';

export type ModelCapability =
  | 'text'
  | 'tools'
  | 'vision'
  | 'reasoning'
  | 'promptCaching';

export interface ModelContextLimits {
  contextWindowTokens: number;
  maxOutputTokens: number;
}

export interface ModelPricingTier {
  /**
   * Upper bound for tier selection based on total prompt/input tokens.
   * Omit this for the final catch-all tier.
   */
  upToInputTokens?: number;
  inputPerMillionUsd: number;
  outputPerMillionUsd: number;
  cacheReadInputPerMillionUsd?: number;
  cacheWriteInputPerMillionUsd?: number;
}

export interface ModelPricing {
  inputPerMillionUsd: number;
  outputPerMillionUsd: number;
  cacheReadInputPerMillionUsd?: number;
  cacheWriteInputPerMillionUsd?: number;
  tiers?: ModelPricingTier[];
  source: string;
  lastVerified: string;
}

export interface ModelDefinition {
  id: string;
  displayName: string;
  provider: ModelProvider;
  apiModel: string;
  aliases: string[];
  context: ModelContextLimits;
  capabilities: ModelCapability[];
  pricing: ModelPricing;
  defaultReasoningEffort?: ReasoningEffort;
  supportedReasoningEfforts?: ReasoningEffort[];
  deprecated?: boolean;
  notes?: string;
}

export interface TokenUsage {
  inputTokens?: number;
  outputTokens?: number;
  cacheReadTokens?: number;
  cacheCreationTokens?: number;
  reasoningTokens?: number;
}

export interface ModelCostBreakdown {
  modelId: string;
  provider: ModelProvider;
  inputCostUsd: number;
  outputCostUsd: number;
  cacheReadCostUsd: number;
  cacheCreationCostUsd: number;
  totalCostUsd: number;
  pricingSource: string;
}
