import { OpenAIProvider } from '../providers/openai';
import { AnthropicProvider } from '../providers/anthropic';
import { GeminiProvider } from '../providers/gemini';
import type { Provider } from '../providers/types';
import {
  getZeroModel,
  assertZeroModelProvider,
} from '../zero-model-registry';
import type { ZeroModelProvider } from '../zero-model-registry';
import type {
  ZeroProviderRuntimeInput,
  ZeroProviderRuntimeKind,
  ZeroProviderRuntimeSource,
  ZeroResolvedProviderRuntime,
} from './types';

const DEFAULT_BASE_URL = {
  openai: 'https://api.openai.com/v1',
  anthropic: 'https://api.anthropic.com',
  google: 'https://generativelanguage.googleapis.com',
} as const satisfies Record<ZeroModelProvider, string>;

const OFFICIAL_OPENAI_BASE_URLS = new Set([
  DEFAULT_BASE_URL.openai,
  'https://api.openai.com',
]);

const MIN_REGISTRY_ANTHROPIC_MAX_TOKENS = 8192;

export class ZeroPendingProviderError extends Error {
  constructor(readonly provider: ZeroProviderRuntimeKind) {
    super(
      `Zero ${provider} provider adapter is not implemented yet. ` +
      'The provider resolver can identify the model, but the streaming adapter lands in a later M1 slice.'
    );
    this.name = 'ZeroPendingProviderError';
  }
}

export function resolveZeroProviderRuntime(
  input: ZeroProviderRuntimeInput
): ZeroResolvedProviderRuntime {
  const requestedModel = normalizeRequired(input.model, 'model');
  const explicitProvider = input.provider;
  const source = input.source ?? 'explicit';
  const baseURL = normalizeOptionalUrl(input.baseURL);
  const registryModel = getZeroModel(requestedModel);

  if (registryModel) {
    if (
      explicitProvider &&
      explicitProvider !== 'openai-compatible' &&
      explicitProvider !== registryModel.provider
    ) {
      assertZeroModelProvider(registryModel.id, explicitProvider);
    }

    const provider = resolveKnownModelProvider(
      registryModel.provider,
      explicitProvider,
      baseURL
    );

    return {
      provider,
      source,
      profileName: input.profileName,
      requestedModel,
      apiModel: registryModel.apiModel,
      baseURL: resolveRuntimeBaseURL(provider, baseURL),
      apiKey: input.apiKey,
      model: registryModel,
      modelId: registryModel.id,
      capabilities: registryModel.capabilities,
    };
  }

  const provider = resolveUnknownModelProvider(explicitProvider, baseURL, source);
  return {
    provider,
    source,
    profileName: input.profileName,
    requestedModel,
    apiModel: requestedModel,
    baseURL: resolveRuntimeBaseURL(provider, baseURL),
    apiKey: input.apiKey,
    capabilities: ['chat', 'streaming', 'system-prompt'],
  };
}

export function createZeroProvider(
  runtime: ZeroResolvedProviderRuntime
): Provider {
  if (runtime.provider === 'openai' || runtime.provider === 'openai-compatible') {
    if (runtime.provider === 'openai' && !runtime.apiKey) {
      throw new Error('Zero openai provider requires an API key');
    }

    return new OpenAIProvider({
      apiKey: runtime.apiKey || 'zero-openai-compatible',
      baseURL: runtime.baseURL,
      model: runtime.apiModel,
    });
  }

  if (runtime.provider === 'anthropic') {
    if (!runtime.apiKey) {
      throw new Error('Zero anthropic provider requires an API key');
    }

    return new AnthropicProvider({
      apiKey: runtime.apiKey,
      baseURL: runtime.baseURL,
      model: runtime.apiModel,
      maxTokens: resolveAnthropicMaxTokens(runtime),
    });
  }

  if (runtime.provider === 'google') {
    if (!runtime.apiKey) {
      throw new Error('Zero google provider requires an API key');
    }

    return new GeminiProvider({
      apiKey: runtime.apiKey,
      baseURL: runtime.baseURL,
      model: runtime.apiModel,
      maxTokens: resolveGoogleMaxTokens(runtime),
    });
  }

  throw new ZeroPendingProviderError(runtime.provider);
}

export function createZeroProviderFromInput(
  input: ZeroProviderRuntimeInput
): {
  runtime: ZeroResolvedProviderRuntime;
  provider: Provider;
} {
  const runtime = resolveZeroProviderRuntime(input);
  return {
    runtime,
    provider: createZeroProvider(runtime),
  };
}

function resolveKnownModelProvider(
  modelProvider: ZeroModelProvider,
  explicitProvider: ZeroProviderRuntimeKind | undefined,
  baseURL: string | undefined
): ZeroProviderRuntimeKind {
  if (explicitProvider === 'openai-compatible') return 'openai-compatible';
  if (modelProvider === 'openai' && baseURL && !OFFICIAL_OPENAI_BASE_URLS.has(baseURL)) {
    return 'openai-compatible';
  }
  return modelProvider;
}

function resolveUnknownModelProvider(
  explicitProvider: ZeroProviderRuntimeKind | undefined,
  baseURL: string | undefined,
  source: ZeroProviderRuntimeSource
): ZeroProviderRuntimeKind {
  if (explicitProvider === 'openai-compatible') {
    ensureOpenAICompatibleGatewayBaseURL(baseURL);
    return 'openai-compatible';
  }
  if (explicitProvider) {
    throw new Error(
      `Unknown Zero model for official ${explicitProvider} provider runtime. ` +
      `Official providers require a model from the Zero model registry. ` +
      `Use provider: "openai-compatible" with a non-official baseURL for custom gateways.`
    );
  }
  if (baseURL && !OFFICIAL_OPENAI_BASE_URLS.has(baseURL)) return 'openai-compatible';

  throw new Error(
    `Unknown Zero model for ${source} provider runtime. ` +
    `Use a model from the Zero model registry or set provider: "openai-compatible" for custom OpenAI-compatible gateways. ` +
    `Valid providers: openai, anthropic, google, openai-compatible.`
  );
}

function resolveRuntimeBaseURL(
  provider: ZeroProviderRuntimeKind,
  baseURL: string | undefined
): string {
  if (provider === 'openai-compatible') {
    ensureOpenAICompatibleGatewayBaseURL(baseURL);
    return baseURL;
  }
  if (provider === 'openai' && (!baseURL || OFFICIAL_OPENAI_BASE_URLS.has(baseURL))) {
    return DEFAULT_BASE_URL.openai;
  }
  return baseURL ?? DEFAULT_BASE_URL[provider];
}

function ensureOpenAICompatibleGatewayBaseURL(baseURL: string | undefined): asserts baseURL is string {
  if (!baseURL || OFFICIAL_OPENAI_BASE_URLS.has(baseURL)) {
    throw new Error(
      'Zero openai-compatible provider requires an explicit non-official baseURL. ' +
      'Use provider: "openai" for the official OpenAI API.'
    );
  }
}

function resolveAnthropicMaxTokens(runtime: ZeroResolvedProviderRuntime): number | undefined {
  if (!runtime.model) return undefined;
  return Math.max(
    runtime.model.context.maxOutputTokens,
    MIN_REGISTRY_ANTHROPIC_MAX_TOKENS
  );
}

function resolveGoogleMaxTokens(runtime: ZeroResolvedProviderRuntime): number | undefined {
  return runtime.model?.context.maxOutputTokens;
}

function normalizeRequired(value: string, label: string): string {
  const normalized = value.trim();
  if (!normalized) throw new Error(`Missing Zero provider ${label}`);
  return normalized;
}

function normalizeOptionalUrl(value: string | undefined): string | undefined {
  if (!value) return undefined;
  const normalized = value.trim().replace(/\/+$/, '');
  try {
    new URL(normalized);
  } catch {
    throw new Error(`Invalid Zero provider baseURL: ${value}`);
  }
  return normalized;
}
