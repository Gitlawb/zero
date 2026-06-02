import { describe, expect, it } from 'bun:test';
import {
  DEFAULT_MODEL_ID,
  assertModelProvider,
  calculateModelCost,
  formatUsd,
  getModel,
  getReasoningEfforts,
  isKnownModel,
  listModels,
  listModelsByCapability,
  listModelsByProvider,
  modelSupportsCapability,
  requireModel,
  resolveModelId,
} from '../src/models';

describe('model registry', () => {
  it('contains the M1 provider families and at least 10 active models', () => {
    const activeModels = listModels();
    const providers = new Set(activeModels.map((model) => model.provider));

    expect(activeModels.length).toBeGreaterThanOrEqual(10);
    expect(providers).toContain('openai');
    expect(providers).toContain('anthropic');
    expect(providers).toContain('google');
  });

  it('resolves canonical IDs and aliases case-insensitively', () => {
    expect(resolveModelId(DEFAULT_MODEL_ID)).toBe(DEFAULT_MODEL_ID);
    expect(resolveModelId('SONNET-4.6')).toBe('claude-sonnet-4-6');
    expect(getModel('gemini-flash')?.id).toBe('gemini-2.5-flash');
    expect(isKnownModel('gpt-5-mini')).toBe(true);
  });

  it('throws a clear error for unknown models', () => {
    expect(() => requireModel('not-a-real-model')).toThrow('Unknown model: not-a-real-model');
  });

  it('filters by provider and capability', () => {
    const anthropicModels = listModelsByProvider('anthropic');
    const visionModels = listModelsByCapability('vision');

    expect(anthropicModels.every((model) => model.provider === 'anthropic')).toBe(true);
    expect(visionModels.every((model) => model.capabilities.includes('vision'))).toBe(true);
    expect(modelSupportsCapability('claude-sonnet', 'tools')).toBe(true);
  });

  it('excludes deprecated models unless requested', () => {
    expect(listModels().some((model) => model.id === 'gpt-4-turbo')).toBe(false);
    expect(listModels({ includeDeprecated: true }).some((model) => model.id === 'gpt-4-turbo')).toBe(true);
  });

  it('validates provider compatibility', () => {
    expect(assertModelProvider('claude-sonnet', 'anthropic').id).toBe('claude-sonnet-4-6');
    expect(() => assertModelProvider('claude-sonnet', 'openai')).toThrow(
      'Model claude-sonnet-4-6 is provided by anthropic, not openai'
    );
  });

  it('exposes reasoning efforts only for reasoning-capable models', () => {
    expect(getReasoningEfforts('claude-sonnet')).toEqual(['low', 'medium', 'high']);
    expect(getReasoningEfforts('gpt-4o')).toEqual([]);
  });

  it('protects registry data from caller mutation', () => {
    const listed = listModels();
    const first = listed[0]!;
    const tiered = getModel('gemini-2.5-pro')!;

    first.aliases.push('mutated-alias');
    tiered.pricing.tiers?.push({
      inputPerMillionUsd: 999,
      outputPerMillionUsd: 999,
    });

    expect(getModel(first.id)?.aliases).not.toContain('mutated-alias');
    expect(getModel('gemini-2.5-pro')?.pricing.tiers?.some((tier) => tier.inputPerMillionUsd === 999)).not.toBe(true);
  });
});

describe('model cost calculation', () => {
  it('calculates input, output, cache, and reasoning costs', () => {
    const cost = calculateModelCost('claude-sonnet', {
      inputTokens: 1_000_000,
      outputTokens: 500_000,
      cacheReadTokens: 100_000,
      cacheCreationTokens: 100_000,
      reasoningTokens: 10_000,
    });

    expect(cost.modelId).toBe('claude-sonnet-4-6');
    expect(cost.provider).toBe('anthropic');
    expect(cost.inputCostUsd).toBe(3);
    expect(cost.outputCostUsd).toBe(7.65);
    expect(cost.cacheReadCostUsd).toBe(0.03);
    expect(cost.cacheCreationCostUsd).toBe(0.375);
    expect(cost.totalCostUsd).toBeCloseTo(11.055);
  });

  it('uses tiered pricing when a model has prompt-size tiers', () => {
    const belowTier = calculateModelCost('gemini-2.5-pro', {
      inputTokens: 200_000,
      outputTokens: 100_000,
    });
    const aboveTier = calculateModelCost('gemini-2.5-pro', {
      inputTokens: 200_001,
      outputTokens: 100_000,
    });

    expect(belowTier.inputCostUsd).toBe(0.25);
    expect(belowTier.outputCostUsd).toBe(1);
    expect(aboveTier.inputCostUsd).toBeCloseTo(0.5000025);
    expect(aboveTier.outputCostUsd).toBe(1.5);
  });

  it('formats tiny and normal USD amounts for UI consumers', () => {
    expect(formatUsd(0)).toBe('$0.00');
    expect(formatUsd(0.0042)).toBe('$0.004200');
    expect(formatUsd(1.234)).toBe('$1.23');
  });
});
