import {
  calculateZeroModelCost,
  formatZeroModelCost,
  requireZeroModel,
} from '../zero-model-registry';
import type { ZeroModelCostBreakdown, ZeroTokenUsage } from '../zero-model-registry';
import type {
  RecordZeroUsageInput,
  ZeroNormalizedUsage,
  ZeroUsageModelSummary,
  ZeroUsageRecord,
  ZeroUsageSummary,
  ZeroUsageTrackerOptions,
} from './types';

export function normalizeZeroUsage(usage: ZeroTokenUsage): ZeroNormalizedUsage {
  const inputTokens = nonNegativeInteger(
    usage.inputTokens ?? usage.promptTokens ?? 0,
    'inputTokens'
  );
  const outputTokens = nonNegativeInteger(
    usage.outputTokens ?? usage.completionTokens ?? 0,
    'outputTokens'
  );
  const cachedInputTokens = Math.min(
    nonNegativeInteger(usage.cachedInputTokens ?? 0, 'cachedInputTokens'),
    inputTokens
  );

  return {
    inputTokens,
    cachedInputTokens,
    outputTokens,
    totalTokens: inputTokens + outputTokens,
  };
}

export class ZeroUsageTracker {
  private readonly now: () => Date;
  private readonly records: ZeroUsageRecord[] = [];
  private nextSequence = 1;

  constructor(options: ZeroUsageTrackerOptions = {}) {
    this.now = options.now ?? (() => new Date());
  }

  recordUsage(input: RecordZeroUsageInput): ZeroUsageRecord {
    const model = requireZeroModel(input.modelId);
    const usage = normalizeZeroUsage(input.usage);
    const cost = calculateZeroModelCost(model, usage);
    const sequence = this.nextSequence;
    this.nextSequence += 1;

    const record: ZeroUsageRecord = {
      id: `zero_usage_${sequence}`,
      sequence,
      modelId: model.id,
      provider: model.provider,
      source: input.source,
      createdAt: this.now().toISOString(),
      usage,
      cost,
    };

    this.records.push(record);
    return record;
  }

  getRecords(): ZeroUsageRecord[] {
    return [...this.records];
  }

  getSummary(): ZeroUsageSummary {
    const summary = createEmptySummary();
    const byModel = new Map<string, ZeroUsageModelSummary>();

    for (const record of this.records) {
      summary.recordCount += 1;
      addUsage(summary, record.usage);
      addCost(summary, record.cost);

      let modelSummary = byModel.get(record.modelId);
      if (!modelSummary) {
        modelSummary = createEmptyModelSummary(record);
        byModel.set(record.modelId, modelSummary);
      }

      modelSummary.recordCount += 1;
      addUsage(modelSummary, record.usage);
      addCost(modelSummary, record.cost);
    }

    summary.formattedTotalCost = formatZeroModelCost(summary.totalCost);
    summary.byModel = Array.from(byModel.values()).map((modelSummary) => ({
      ...modelSummary,
      formattedTotalCost: formatZeroModelCost(modelSummary.totalCost),
    }));

    const lastRecord = this.records.at(-1);
    if (lastRecord) {
      summary.lastRecord = lastRecord;
    }

    return summary;
  }

  reset(): void {
    this.records.length = 0;
    this.nextSequence = 1;
  }
}

export function formatZeroUsageSummary(summary: ZeroUsageSummary): string {
  const requestLabel = summary.recordCount === 1 ? 'request' : 'requests';
  const tokens = new Intl.NumberFormat('en-US').format(summary.totalTokens);
  const requests = new Intl.NumberFormat('en-US').format(summary.recordCount);
  return `${requests} ${requestLabel}, ${tokens} tokens, ${summary.formattedTotalCost}`;
}

function createEmptySummary(): ZeroUsageSummary {
  return {
    recordCount: 0,
    currency: 'USD',
    inputTokens: 0,
    cachedInputTokens: 0,
    outputTokens: 0,
    totalTokens: 0,
    inputCost: 0,
    cachedInputCost: 0,
    outputCost: 0,
    totalCost: 0,
    formattedTotalCost: formatZeroModelCost(0),
    byModel: [],
  };
}

function createEmptyModelSummary(record: ZeroUsageRecord): ZeroUsageModelSummary {
  return {
    modelId: record.modelId,
    provider: record.provider,
    recordCount: 0,
    inputTokens: 0,
    cachedInputTokens: 0,
    outputTokens: 0,
    totalTokens: 0,
    inputCost: 0,
    cachedInputCost: 0,
    outputCost: 0,
    totalCost: 0,
    formattedTotalCost: formatZeroModelCost(0),
  };
}

function addUsage(
  target: Pick<
    ZeroUsageSummary | ZeroUsageModelSummary,
    'inputTokens' | 'cachedInputTokens' | 'outputTokens' | 'totalTokens'
  >,
  usage: ZeroNormalizedUsage
): void {
  target.inputTokens += usage.inputTokens;
  target.cachedInputTokens += usage.cachedInputTokens;
  target.outputTokens += usage.outputTokens;
  target.totalTokens += usage.totalTokens;
}

function addCost(
  target: Pick<
    ZeroUsageSummary | ZeroUsageModelSummary,
    'inputCost' | 'cachedInputCost' | 'outputCost' | 'totalCost'
  >,
  cost: ZeroModelCostBreakdown
): void {
  target.inputCost += cost.inputCost;
  target.cachedInputCost += cost.cachedInputCost;
  target.outputCost += cost.outputCost;
  target.totalCost += cost.totalCost;
}

function nonNegativeInteger(value: number, label: string): number {
  if (!Number.isFinite(value) || value < 0) {
    throw new Error(`Expected ${label} to be a non-negative number`);
  }
  return Math.floor(value);
}
