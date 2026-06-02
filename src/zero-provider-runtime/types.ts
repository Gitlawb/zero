import type {
  ZeroModelCapability,
  ZeroModelDefinition,
  ZeroModelProvider,
} from '../zero-model-registry';

export type ZeroProviderRuntimeKind = ZeroModelProvider | 'openai-compatible';

export type ZeroProviderRuntimeSource =
  | 'profile'
  | 'provider-command'
  | 'environment'
  | 'explicit';

export interface ZeroProviderRuntimeInput {
  provider?: ZeroProviderRuntimeKind;
  apiKey?: string;
  baseURL?: string;
  /** User-supplied model string. `modelId` on the resolved runtime is registry-canonical when known. */
  model: string;
  profileName?: string;
  source?: ZeroProviderRuntimeSource;
}

export interface ZeroResolvedProviderRuntime {
  provider: ZeroProviderRuntimeKind;
  source: ZeroProviderRuntimeSource;
  profileName?: string;
  requestedModel: string;
  apiModel: string;
  baseURL: string;
  apiKey?: string;
  model?: ZeroModelDefinition;
  modelId?: string;
  capabilities: readonly ZeroModelCapability[];
}
