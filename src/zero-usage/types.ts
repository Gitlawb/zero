import type {
  ZeroModelCostBreakdown,
  ZeroModelProvider,
  ZeroTokenUsage,
} from '../zero-model-registry';

export interface ZeroNormalizedUsage {
  inputTokens: number;
  cachedInputTokens: number;
  outputTokens: number;
  totalTokens: number;
}

export interface RecordZeroUsageInput {
  modelId: string;
  usage: ZeroTokenUsage;
  source?: string;
}

export interface ZeroUsageRecord {
  id: string;
  sequence: number;
  modelId: string;
  provider: ZeroModelProvider;
  source?: string;
  createdAt: string;
  usage: ZeroNormalizedUsage;
  cost: ZeroModelCostBreakdown;
}

export interface ZeroUsageModelSummary {
  modelId: string;
  provider: ZeroModelProvider;
  recordCount: number;
  inputTokens: number;
  cachedInputTokens: number;
  outputTokens: number;
  totalTokens: number;
  inputCost: number;
  cachedInputCost: number;
  outputCost: number;
  totalCost: number;
  formattedTotalCost: string;
}

export interface ZeroUsageSummary {
  recordCount: number;
  currency: 'USD';
  inputTokens: number;
  cachedInputTokens: number;
  outputTokens: number;
  totalTokens: number;
  inputCost: number;
  cachedInputCost: number;
  outputCost: number;
  totalCost: number;
  formattedTotalCost: string;
  byModel: ZeroUsageModelSummary[];
  lastRecord?: ZeroUsageRecord;
}

export interface ZeroUsageTrackerOptions {
  now?: () => Date;
}
