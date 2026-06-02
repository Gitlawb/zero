import { describe, expect, it } from 'bun:test';
import {
  ProviderFactoryError,
  assertProviderSupportsModel,
  createProvider,
  resolveApiModel,
  resolveProviderType,
} from '../src/providers/factory';
import { OpenAIProvider } from '../src/providers/openai';

describe('provider factory', () => {
  it('resolves explicit provider type first', () => {
    expect(resolveProviderType({
      provider: 'openai-compatible',
      baseURL: 'https://gateway.example.com/v1',
      model: 'custom-model',
    })).toBe('openai-compatible');
  });

  it('resolves provider type from known registry models', () => {
    expect(resolveProviderType({
      baseURL: 'https://api.anthropic.com',
      model: 'claude-sonnet',
    })).toBe('anthropic');

    expect(resolveProviderType({
      baseURL: 'https://generativelanguage.googleapis.com/v1beta',
      model: 'gemini-flash',
    })).toBe('google');
  });

  it('falls back to OpenAI-compatible for unknown custom endpoint models', () => {
    expect(resolveProviderType({
      baseURL: 'https://gateway.example.com/v1',
      model: 'unknown-but-valid-custom-model',
    })).toBe('openai-compatible');
  });

  it('falls back to OpenAI for unknown models on the default OpenAI base URL', () => {
    expect(resolveProviderType({
      baseURL: 'https://api.openai.com/v1',
      model: 'unknown-openai-model',
    })).toBe('openai');
  });

  it('resolves registry aliases to API model IDs', () => {
    expect(resolveApiModel('gpt-5-mini')).toBe('gpt-5.4-mini');
    expect(resolveApiModel('unknown-custom-model')).toBe('unknown-custom-model');
  });

  it('creates the OpenAI provider for OpenAI-compatible configs', () => {
    const provider = createProvider({
      provider: 'openai-compatible',
      baseURL: 'https://gateway.example.com/v1',
      apiKey: 'test-key',
      model: 'custom-model',
    });

    expect(provider).toBeInstanceOf(OpenAIProvider);
  });

  it('throws a clear error for reserved providers that do not have modules yet', () => {
    expect(() => createProvider({
      provider: 'anthropic',
      baseURL: 'https://api.anthropic.com',
      apiKey: 'test-key',
      model: 'claude-sonnet',
    })).toThrow('Anthropic provider is not implemented yet');
  });

  it('rejects known registry models configured under the wrong provider', () => {
    expect(() => assertProviderSupportsModel('openai', 'claude-sonnet')).toThrow(
      'Model claude-sonnet-4-6 is provided by anthropic, not openai.'
    );
  });

  it('allows unknown models because custom gateways can expose non-registry IDs', () => {
    expect(() => assertProviderSupportsModel('openai-compatible', 'vendor/custom-model')).not.toThrow();
  });

  it('uses a typed factory error for unsupported providers', () => {
    try {
      createProvider({
        provider: 'google',
        baseURL: 'https://generativelanguage.googleapis.com/v1beta',
        model: 'gemini-flash',
      });
      throw new Error('Expected createProvider to throw');
    } catch (error) {
      expect(error).toBeInstanceOf(ProviderFactoryError);
      expect((error as ProviderFactoryError).providerType).toBe('google');
    }
  });
});
