import { requireZeroModel } from './registry';
import type {
  ZeroModelCostBreakdown,
  ZeroModelDefinition,
  ZeroModelPricing,
  ZeroModelPricingTier,
  ZeroTokenUsage,
} from './types';

const TOKENS_PER_MILLION = 1_000_000;

/**
 * Calculates estimated USD model cost from provider token usage.
 *
 * Usage token names accept both Zero's normalized `inputTokens`/`outputTokens`
 * and the current provider event aliases `promptTokens`/`completionTokens`.
 * Cached input tokens are billed only when the model exposes cache pricing;
 * otherwise they are treated as ordinary input tokens.
 */
export function calculateZeroModelCost(
  modelOrAlias: string | ZeroModelDefinition,
  usage: ZeroTokenUsage
): ZeroModelCostBreakdown {
  const model =
    typeof modelOrAlias === 'string' ? requireZeroModel(modelOrAlias) : modelOrAlias;
  const inputTokens = nonNegativeInteger(
    usage.inputTokens ?? usage.promptTokens ?? 0,
    'inputTokens'
  );
  const outputTokens = nonNegativeInteger(
    usage.outputTokens ?? usage.completionTokens ?? 0,
    'outputTokens'
  );
  const requestedCachedInputTokens = Math.min(
    nonNegativeInteger(usage.cachedInputTokens ?? 0, 'cachedInputTokens'),
    inputTokens
  );
  const tier = selectZeroPricingTier(model.pricing, inputTokens);
  const inputRate = getInputRate(model.pricing, tier);
  const outputRate = getOutputRate(model.pricing, tier);
  const cachedInputRate = getCachedInputRate(model.pricing, tier);
  const cachedInputTokens =
    cachedInputRate === undefined ? 0 : requestedCachedInputTokens;
  const uncachedInputTokens = inputTokens - cachedInputTokens;
  const inputCost = costForTokens(uncachedInputTokens, inputRate);
  const cachedInputCost = costForTokens(cachedInputTokens, cachedInputRate ?? 0);
  const outputCost = costForTokens(outputTokens, outputRate);

  return {
    modelId: model.id,
    provider: model.provider,
    currency: 'USD',
    inputTokens,
    cachedInputTokens,
    outputTokens,
    inputCost,
    cachedInputCost,
    outputCost,
    totalCost: inputCost + cachedInputCost + outputCost,
    pricingTier: tier,
  };
}

export function formatZeroModelCost(cost: number): string {
  if (!Number.isFinite(cost) || cost < 0) {
    throw new Error(`Invalid Zero model cost: ${cost}`);
  }

  const fractionDigits = cost > 0 && cost < 0.01 ? 6 : 4;
  return new Intl.NumberFormat('en-US', {
    style: 'currency',
    currency: 'USD',
    minimumFractionDigits: fractionDigits,
    maximumFractionDigits: fractionDigits,
  }).format(cost);
}

function selectZeroPricingTier(
  pricing: ZeroModelPricing,
  inputTokens: number
): ZeroModelPricingTier | undefined {
  if (!pricing.tiers?.length) return undefined;

  const boundedTiers = pricing.tiers
    .filter((tier) => tier.upToInputTokens !== undefined)
    .sort((a, b) => a.upToInputTokens! - b.upToInputTokens!);

  for (const tier of boundedTiers) {
    if (inputTokens <= tier.upToInputTokens!) {
      return tier;
    }
  }

  const fallbackTier = pricing.tiers.find(
    (tier) => tier.upToInputTokens === undefined
  );
  if (!fallbackTier) {
    throw new Error(`No Zero model pricing tier covers ${inputTokens} input tokens`);
  }

  return fallbackTier;
}

function getInputRate(
  pricing: ZeroModelPricing,
  tier: ZeroModelPricingTier | undefined
): number {
  return requireRate(tier?.inputPerMillion ?? pricing.inputPerMillion, 'input');
}

function getOutputRate(
  pricing: ZeroModelPricing,
  tier: ZeroModelPricingTier | undefined
): number {
  return requireRate(tier?.outputPerMillion ?? pricing.outputPerMillion, 'output');
}

function getCachedInputRate(
  pricing: ZeroModelPricing,
  tier: ZeroModelPricingTier | undefined
): number | undefined {
  return tier?.cachedInputPerMillion ?? pricing.cachedInputPerMillion;
}

function costForTokens(tokens: number, perMillionRate: number): number {
  return (tokens / TOKENS_PER_MILLION) * perMillionRate;
}

function requireRate(rate: number | undefined, label: string): number {
  if (rate === undefined) {
    throw new Error(`Missing Zero model ${label} pricing rate`);
  }
  if (!Number.isFinite(rate) || rate < 0) {
    throw new Error(`Invalid Zero model ${label} pricing rate: ${rate}`);
  }
  return rate;
}

function nonNegativeInteger(value: number, label: string): number {
  if (!Number.isFinite(value) || value < 0) {
    throw new Error(`Expected ${label} to be a non-negative number`);
  }
  return Math.floor(value);
}
