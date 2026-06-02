import { describe, expect, it } from 'bun:test';
import {
  ZERO_DEFAULT_MODEL_ID,
  calculateZeroModelCost,
  formatZeroModelCost,
  getZeroModel,
  getZeroReasoningEfforts,
  isKnownZeroModel,
  listZeroModels,
  listZeroModelsByCapability,
  listZeroModelsByProvider,
  resolveZeroModelId,
  zeroModelSupportsCapability,
} from '../src/zero-model-registry';
import type { ZeroModelProvider } from '../src/zero-model-registry';

describe('Zero model registry', () => {
  it('contains at least 10 active or preview models across required providers', () => {
    const models = listZeroModels();
    expect(models.length).toBeGreaterThanOrEqual(10);
    expect(models.some((model) => model.status === 'deprecated')).toBe(false);
    expect(listZeroModels({ includeDeprecated: true }).some(
      (model) => model.id === 'gpt-4-turbo'
    )).toBe(true);

    const providers = new Set<ZeroModelProvider>(models.map((model) => model.provider));
    expect(providers).toEqual(new Set(['openai', 'anthropic', 'google']));
  });

  it('exposes complete model metadata for consumers', () => {
    for (const model of listZeroModels()) {
      expect(model.id.length).toBeGreaterThan(0);
      expect(model.displayName.length).toBeGreaterThan(0);
      expect(model.apiModel.length).toBeGreaterThan(0);
      expect(model.context.contextWindow).toBeGreaterThan(0);
      expect(model.context.maxOutputTokens).toBeGreaterThan(0);
      expect(model.capabilities).toContain('chat');
      expect(model.capabilities).toContain('streaming');
      expect(model.pricing.currency).toBe('USD');
      expect(model.pricing.unit).toBe('per_1m_tokens');
      expect(model.pricing.source).toMatch(/^https:\/\//);
      expect(model.pricing.sourceLastVerified).toMatch(/^\d{4}-\d{2}-\d{2}$/);
    }
  });

  it('resolves ids and aliases case-insensitively', () => {
    expect(resolveZeroModelId(' GPT-5.4 ')).toBe('gpt-5.4');
    expect(resolveZeroModelId('OPENAI:GPT-5.4')).toBe('gpt-5.4');
    expect(resolveZeroModelId('sonnet-4.6')).toBe('claude-sonnet-4.6');
    expect(resolveZeroModelId('gemini-flash')).toBe('gemini-2.5-flash');
    expect(isKnownZeroModel('unknown-model')).toBe(false);
  });

  it('filters models by provider and capability', () => {
    expect(listZeroModelsByProvider('openai').every((model) => model.provider === 'openai')).toBe(true);
    expect(listZeroModelsByProvider('anthropic').length).toBeGreaterThan(0);
    expect(listZeroModelsByProvider('google').length).toBeGreaterThan(0);

    const visionModels = listZeroModelsByCapability('vision');
    expect(visionModels.length).toBeGreaterThan(0);
    expect(visionModels.every((model) => model.capabilities.includes('vision'))).toBe(true);
    expect(zeroModelSupportsCapability('gpt-5.4', 'reasoning')).toBe(true);
  });

  it('provides reasoning efforts for reasoning-capable models', () => {
    expect(ZERO_DEFAULT_MODEL_ID).toBe('gpt-5.4');
    expect(getZeroReasoningEfforts(ZERO_DEFAULT_MODEL_ID)).toEqual([
      'none',
      'low',
      'medium',
      'high',
      'xhigh',
    ]);
    expect(getZeroReasoningEfforts('gpt-4o')).toEqual([]);
  });
});

describe('Zero model cost helpers', () => {
  it('calculates cost from input, cached input, and output tokens', () => {
    const cost = calculateZeroModelCost('gpt-5.4', {
      inputTokens: 1_000_000,
      cachedInputTokens: 100_000,
      outputTokens: 500_000,
    });

    expect(cost.modelId).toBe('gpt-5.4');
    expect(cost.inputCost).toBeCloseTo(4.5);
    expect(cost.cachedInputCost).toBeCloseTo(0.05);
    expect(cost.outputCost).toBeCloseTo(11.25);
    expect(cost.totalCost).toBeCloseTo(15.8);
  });

  it('uses prompt and completion token aliases from provider usage events', () => {
    const cost = calculateZeroModelCost('haiku-4.5', {
      promptTokens: 2_000,
      completionTokens: 1_000,
    });

    expect(cost.inputTokens).toBe(2_000);
    expect(cost.outputTokens).toBe(1_000);
    expect(cost.totalCost).toBeCloseTo(0.007);
  });

  it('selects the correct tier for Gemini Pro long prompts', () => {
    const shortPrompt = calculateZeroModelCost('gemini-2.5-pro', {
      inputTokens: 200_000,
      outputTokens: 1_000,
    });
    const longPrompt = calculateZeroModelCost('gemini-2.5-pro', {
      inputTokens: 200_001,
      outputTokens: 1_000,
    });

    expect(shortPrompt.pricingTier?.inputPerMillion).toBe(1.25);
    expect(longPrompt.pricingTier?.inputPerMillion).toBe(2.5);
    expect(longPrompt.totalCost).toBeGreaterThan(shortPrompt.totalCost);
  });

  it('formats small and regular USD costs for UI display', () => {
    expect(formatZeroModelCost(0.000123)).toBe('$0.000123');
    expect(formatZeroModelCost(1.23456)).toBe('$1.2346');
  });

  it('returns registry model objects for direct downstream use', () => {
    const model = getZeroModel('gemini-flash');
    expect(model?.id).toBe('gemini-2.5-flash');
    expect(model?.pricing.source).toContain('ai.google.dev');
  });
});
