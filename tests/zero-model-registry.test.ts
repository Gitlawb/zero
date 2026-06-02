import { describe, expect, it } from 'bun:test';
import {
  ZERO_DEFAULT_MODEL_ID,
  assertZeroModelProvider,
  calculateZeroModelCost,
  formatZeroModelCost,
  getZeroModel,
  getZeroReasoningEfforts,
  isKnownZeroModel,
  listZeroModels,
  listZeroModelsByCapability,
  listZeroModelsByProvider,
  requireZeroModel,
  resolveZeroModelId,
  zeroModelSupportsCapability,
} from '../src/zero-model-registry';
import type { ZeroModelProvider } from '../src/zero-model-registry';

describe('Zero model registry', () => {
  it('contains at least 10 active or preview models across required providers', () => {
    const models = listZeroModels();
    expect(models.length).toBeGreaterThanOrEqual(10);
    expect(models.some((model) => model.status === 'deprecated')).toBe(false);
    expect(models.some((model) => model.id === 'gpt-4-turbo')).toBe(false);
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
    expect(resolveZeroModelId(' GPT-4.1 ')).toBe('gpt-4.1');
    expect(resolveZeroModelId('OPENAI:GPT-4.1')).toBe('gpt-4.1');
    expect(resolveZeroModelId('sonnet-4.5')).toBe('claude-sonnet-4.5');
    expect(resolveZeroModelId('gemini-flash')).toBe('gemini-2.5-flash');
    expect(isKnownZeroModel('unknown-model')).toBe(false);
  });

  it('keeps aliases stable and avoids speculative latest redirects', () => {
    expect(resolveZeroModelId('gpt-latest')).toBeUndefined();
    expect(resolveZeroModelId('gpt-5-mini')).toBeUndefined();
    expect(resolveZeroModelId('gemini-2.5-pro-latest')).toBeUndefined();
  });

  it('filters models by provider and capability', () => {
    expect(listZeroModelsByProvider('openai').every((model) => model.provider === 'openai')).toBe(true);
    expect(listZeroModelsByProvider('anthropic').length).toBeGreaterThan(0);
    expect(listZeroModelsByProvider('google').length).toBeGreaterThan(0);

    const visionModels = listZeroModelsByCapability('vision');
    expect(visionModels.length).toBeGreaterThan(0);
    expect(visionModels.every((model) => model.capabilities.includes('vision'))).toBe(true);
    expect(zeroModelSupportsCapability('gemini-2.5-pro', 'reasoning')).toBe(true);
  });

  it('provides reasoning efforts for reasoning-capable models', () => {
    expect(ZERO_DEFAULT_MODEL_ID).toBe('gpt-4.1');
    expect(getZeroReasoningEfforts('gemini-2.5-pro')).toEqual([
      'low',
      'medium',
      'high',
    ]);
    expect(getZeroReasoningEfforts('gpt-4o')).toEqual([]);
  });

  it('throws for unknown required models and provider mismatches', () => {
    expect(() => requireZeroModel('unknown-model')).toThrow('Unknown Zero model');
    expect(assertZeroModelProvider('gpt-4.1', 'openai').id).toBe('gpt-4.1');
    expect(() => assertZeroModelProvider('gpt-4.1', 'anthropic')).toThrow(
      'belongs to openai'
    );
  });
});

describe('Zero model cost helpers', () => {
  it('calculates cost from input, cached input, and output tokens', () => {
    const cost = calculateZeroModelCost('gpt-4.1', {
      inputTokens: 1_000_000,
      cachedInputTokens: 100_000,
      outputTokens: 500_000,
    });

    expect(cost.modelId).toBe('gpt-4.1');
    expect(cost.inputCost).toBeCloseTo(1.8);
    expect(cost.cachedInputCost).toBeCloseTo(0.05);
    expect(cost.outputCost).toBeCloseTo(4);
    expect(cost.totalCost).toBeCloseTo(5.85);
  });

  it('supports direct model definitions and fully cached input', () => {
    const model = requireZeroModel('gpt-4.1-mini');
    const cost = calculateZeroModelCost(model, {
      inputTokens: 1_000_000,
      cachedInputTokens: 1_000_000,
      outputTokens: 0,
    });

    expect(cost.inputCost).toBe(0);
    expect(cost.cachedInputCost).toBeCloseTo(0.1);
    expect(cost.totalCost).toBeCloseTo(0.1);
  });

  it('uses prompt and completion token aliases from provider usage events', () => {
    const cost = calculateZeroModelCost('haiku-3.5', {
      promptTokens: 2_000.9,
      completionTokens: 1_000.1,
    });

    expect(cost.inputTokens).toBe(2_000);
    expect(cost.outputTokens).toBe(1_000);
    expect(cost.totalCost).toBeCloseTo(0.0056);
  });

  it('ignores cached input tokens for models without cache pricing', () => {
    const cost = calculateZeroModelCost('gpt-4-turbo', {
      inputTokens: 1_000,
      cachedInputTokens: 1_000,
      outputTokens: 1_000,
    });

    expect(cost.cachedInputTokens).toBe(0);
    expect(cost.inputCost).toBeCloseTo(0.01);
    expect(cost.cachedInputCost).toBe(0);
    expect(cost.outputCost).toBeCloseTo(0.03);
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
    expect(() => formatZeroModelCost(-1)).toThrow('Invalid Zero model cost');
    expect(() => formatZeroModelCost(Number.NaN)).toThrow('Invalid Zero model cost');
  });

  it('returns registry model objects for direct downstream use', () => {
    const model = getZeroModel('gemini-flash');
    expect(model?.id).toBe('gemini-2.5-flash');
    expect(model?.pricing.source).toContain('ai.google.dev');
  });
});
