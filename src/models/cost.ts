import { requireModel } from './registry';
import type {
  ModelCostBreakdown,
  ModelPricing,
  ModelPricingTier,
  TokenUsage,
} from './types';

const TOKENS_PER_MILLION = 1_000_000;

export function calculateModelCost(
  modelIdOrAlias: string,
  usage: TokenUsage
): ModelCostBreakdown {
  const model = requireModel(modelIdOrAlias);
  const inputTokens = usage.inputTokens ?? 0;
  const outputTokens = usage.outputTokens ?? 0;
  const cacheReadTokens = usage.cacheReadTokens ?? 0;
  const cacheCreationTokens = usage.cacheCreationTokens ?? 0;
  const reasoningTokens = usage.reasoningTokens ?? 0;
  const totalPromptTokens = inputTokens + cacheReadTokens + cacheCreationTokens;
  const pricing = selectPricing(model.pricing, totalPromptTokens);

  const inputCostUsd = priceTokens(inputTokens, pricing.inputPerMillionUsd);
  const outputCostUsd = priceTokens(
    outputTokens + reasoningTokens,
    pricing.outputPerMillionUsd
  );
  const cacheReadCostUsd = priceTokens(
    cacheReadTokens,
    pricing.cacheReadInputPerMillionUsd ?? pricing.inputPerMillionUsd
  );
  const cacheCreationCostUsd = priceTokens(
    cacheCreationTokens,
    pricing.cacheWriteInputPerMillionUsd ?? pricing.inputPerMillionUsd
  );

  return {
    modelId: model.id,
    provider: model.provider,
    inputCostUsd,
    outputCostUsd,
    cacheReadCostUsd,
    cacheCreationCostUsd,
    totalCostUsd: inputCostUsd + outputCostUsd + cacheReadCostUsd + cacheCreationCostUsd,
    pricingSource: model.pricing.source,
  };
}

export function formatUsd(amount: number): string {
  if (amount === 0) return '$0.00';
  if (amount < 0.01) return `$${amount.toFixed(6)}`;
  return `$${amount.toFixed(2)}`;
}

function selectPricing(pricing: ModelPricing, promptTokens: number): ModelPricingTier {
  if (!pricing.tiers || pricing.tiers.length === 0) {
    return {
      inputPerMillionUsd: pricing.inputPerMillionUsd,
      outputPerMillionUsd: pricing.outputPerMillionUsd,
      cacheReadInputPerMillionUsd: pricing.cacheReadInputPerMillionUsd,
      cacheWriteInputPerMillionUsd: pricing.cacheWriteInputPerMillionUsd,
    };
  }

  const matched = pricing.tiers.find((tier) =>
    tier.upToInputTokens === undefined || promptTokens <= tier.upToInputTokens
  );

  if (!matched) {
    return pricing.tiers[pricing.tiers.length - 1]!;
  }

  return matched;
}

function priceTokens(tokens: number, pricePerMillion: number): number {
  return (tokens / TOKENS_PER_MILLION) * pricePerMillion;
}
