import type { ProviderConfig } from '../config/provider';
import type { ProviderType } from '../config/types';
import { getModel } from '../models';
import type { ModelProvider } from '../models';
import { OpenAIProvider } from './openai';
import type { Provider } from './types';

const OPENAI_BASE_URL = 'https://api.openai.com/v1';

export class ProviderFactoryError extends Error {
  constructor(message: string, readonly providerType?: ProviderType) {
    super(message);
    this.name = 'ProviderFactoryError';
  }
}

export function createProvider(config: ProviderConfig): Provider {
  const providerType = resolveProviderType(config);
  assertProviderSupportsModel(providerType, config.model);
  const apiModel = resolveApiModel(config.model);

  if (providerType === 'openai' || providerType === 'openai-compatible') {
    return new OpenAIProvider({
      apiKey: config.apiKey || '',
      baseURL: config.baseURL || OPENAI_BASE_URL,
      model: apiModel,
    });
  }

  throw new ProviderFactoryError(
    `${displayProvider(providerType)} provider is not implemented yet. ` +
      'This provider type is reserved for the upcoming M1 provider modules.',
    providerType
  );
}

export function resolveProviderType(config: ProviderConfig): ProviderType {
  if (config.provider) return config.provider;

  const registryModel = getModel(config.model);
  if (registryModel) return registryModel.provider;

  return isDefaultOpenAIBaseURL(config.baseURL) ? 'openai' : 'openai-compatible';
}

export function resolveApiModel(modelIdOrAlias: string): string {
  return getModel(modelIdOrAlias)?.apiModel ?? modelIdOrAlias;
}

export function assertProviderSupportsModel(
  providerType: ProviderType,
  modelIdOrAlias: string
): void {
  const registryModel = getModel(modelIdOrAlias);
  if (!registryModel) return;

  const expectedProvider = normalizeProvider(providerType);
  if (registryModel.provider !== expectedProvider) {
    throw new ProviderFactoryError(
      `Model ${registryModel.id} is provided by ${registryModel.provider}, not ${providerType}.`,
      providerType
    );
  }
}

function normalizeProvider(providerType: ProviderType): ModelProvider {
  return providerType === 'openai-compatible' ? 'openai' : providerType;
}

function isDefaultOpenAIBaseURL(baseURL: string): boolean {
  return baseURL.replace(/\/+$/, '') === OPENAI_BASE_URL;
}

function displayProvider(providerType: ProviderType): string {
  if (providerType === 'openai-compatible') return 'OpenAI-compatible';
  return providerType[0]!.toUpperCase() + providerType.slice(1);
}
