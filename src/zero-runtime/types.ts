import type {
  AgentOptions,
  AgentPermissionMode,
} from '../agent/loop';
import type { ProviderConfig } from '../config/provider';
import type { Provider } from '../providers/types';
import type {
  ZeroModelDefinition,
  ZeroModelProfile,
  ZeroReasoningEffort,
} from '../zero-model-registry';
import type { ZeroResolvedProviderRuntime } from '../zero-provider-runtime';

export type ZeroAutonomyLevel = 'low' | 'medium' | 'high';
export type ZeroRuntimeSurface = 'tui' | 'exec';

export interface ZeroRuntimeOptions {
  surface: ZeroRuntimeSurface;
  model?: string;
  modelProfile?: string;
  reasoningEffort?: string;
  autonomy?: string;
  permissionMode?: AgentPermissionMode;
  skipPermissionsUnsafe?: boolean;
  enabledTools?: readonly string[];
  disabledTools?: readonly string[];
  maxTurns?: number;
}

export interface ZeroRunContext {
  surface: ZeroRuntimeSurface;
  providerConfig: ProviderConfig;
  runtime: ZeroResolvedProviderRuntime;
  provider: Provider;
  model?: ZeroModelDefinition;
  modelProfile?: ZeroModelProfile;
  modelId: string;
  modelLabel: string;
  providerLabel: string;
  reasoningEffort?: ZeroReasoningEffort;
  autonomy: ZeroAutonomyLevel;
  permissionMode: AgentPermissionMode;
  enabledTools?: readonly string[];
  disabledTools?: readonly string[];
  agentOptions: Pick<
    AgentOptions,
    'maxTurns' | 'permissionMode' | 'enabledTools' | 'disabledTools' | 'reasoningEffort' | 'autonomy'
  >;
}

export class ZeroRuntimeUsageError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'ZeroRuntimeUsageError';
  }
}

export class ZeroRuntimeProviderError extends Error {
  constructor(message: string, options?: { cause?: unknown }) {
    super(message, options);
    this.name = 'ZeroRuntimeProviderError';
  }
}
