import type { ModelProvider } from '../models';

export type ProviderType = ModelProvider | 'openai-compatible';

export interface ProviderProfile {
  name: string;
  provider?: ProviderType;
  baseURL: string;
  apiKey?: string;
  model: string;
  description?: string;
}

export interface ZeroConfig {
  activeProvider?: string;
  providers: ProviderProfile[];
}
