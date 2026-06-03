import { describe, expect, it } from 'bun:test';
import {
  ZeroUsageTracker,
  formatZeroUsageSummary,
  normalizeZeroUsage,
} from '../src/zero-usage';

describe('normalizeZeroUsage', () => {
  it('normalizes provider token aliases and floors fractional counts', () => {
    expect(normalizeZeroUsage({
      promptTokens: 1_000.9,
      cachedInputTokens: 200.7,
      completionTokens: 500.4,
    })).toEqual({
      inputTokens: 1_000,
      cachedInputTokens: 200,
      outputTokens: 500,
      totalTokens: 1_500,
    });
  });

  it('clamps cached input to input tokens', () => {
    expect(normalizeZeroUsage({
      inputTokens: 25,
      cachedInputTokens: 100,
      outputTokens: 10,
    })).toEqual({
      inputTokens: 25,
      cachedInputTokens: 25,
      outputTokens: 10,
      totalTokens: 35,
    });
  });

  it('rejects invalid token counts before they reach cost tracking', () => {
    expect(() => normalizeZeroUsage({ inputTokens: -1 })).toThrow(
      'Expected inputTokens to be a non-negative number'
    );
    expect(() => normalizeZeroUsage({ completionTokens: Number.NaN })).toThrow(
      'Expected outputTokens to be a non-negative number'
    );
  });
});

describe('ZeroUsageTracker', () => {
  it('records model usage with cost breakdowns from the model registry', () => {
    const tracker = new ZeroUsageTracker({
      now: () => new Date('2026-06-03T05:30:00.000Z'),
    });

    const record = tracker.recordUsage({
      modelId: 'gpt-4.1',
      source: 'agent-loop',
      usage: {
        promptTokens: 1_000,
        cachedInputTokens: 200,
        completionTokens: 500,
      },
    });

    expect(record).toMatchObject({
      id: 'zero_usage_1',
      sequence: 1,
      modelId: 'gpt-4.1',
      provider: 'openai',
      source: 'agent-loop',
      createdAt: '2026-06-03T05:30:00.000Z',
      usage: {
        inputTokens: 1_000,
        cachedInputTokens: 200,
        outputTokens: 500,
        totalTokens: 1_500,
      },
    });
    expect(record.cost.totalCost).toBeCloseTo(0.0057);

    const summary = tracker.getSummary();
    expect(summary.recordCount).toBe(1);
    expect(summary.totalTokens).toBe(1_500);
    expect(summary.totalCost).toBeCloseTo(0.0057);
    expect(summary.formattedTotalCost).toBe('$0.005700');
    expect(summary.byModel).toHaveLength(1);
    expect(summary.byModel[0]).toMatchObject({
      modelId: 'gpt-4.1',
      provider: 'openai',
      recordCount: 1,
      totalTokens: 1_500,
      formattedTotalCost: '$0.005700',
    });
  });

  it('aggregates records by model and formats a compact summary', () => {
    const tracker = new ZeroUsageTracker();
    tracker.recordUsage({
      modelId: 'gpt-4.1',
      usage: { inputTokens: 1_000, outputTokens: 1_000 },
    });
    tracker.recordUsage({
      modelId: 'gpt-4.1-mini',
      usage: { inputTokens: 500, outputTokens: 500 },
    });

    const summary = tracker.getSummary();
    expect(summary.recordCount).toBe(2);
    expect(summary.totalTokens).toBe(3_000);
    expect(summary.totalCost).toBeCloseTo(0.011);
    expect(summary.byModel.map((model) => model.modelId)).toEqual([
      'gpt-4.1',
      'gpt-4.1-mini',
    ]);
    expect(formatZeroUsageSummary(summary)).toBe(
      '2 requests, 3,000 tokens, $0.0110'
    );
  });

  it('rejects unknown models instead of producing unpriced records', () => {
    const tracker = new ZeroUsageTracker();

    expect(() => tracker.recordUsage({
      modelId: 'unknown-zero-model',
      usage: { inputTokens: 1, outputTokens: 1 },
    })).toThrow('Unknown Zero model');
  });
});
