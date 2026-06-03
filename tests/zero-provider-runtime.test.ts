import { describe, expect, it } from 'bun:test';
import {
  createZeroProvider,
  resolveZeroProviderRuntime,
  ZeroPendingProviderError,
} from '../src/zero-provider-runtime';

describe('resolveZeroProviderRuntime', () => {
  it('resolves a registry model to its provider and API model', () => {
    const runtime = resolveZeroProviderRuntime({
      model: 'openai:gpt-4.1',
      apiKey: 'test-key',
      source: 'environment',
    });

    expect(runtime.provider).toBe('openai');
    expect(runtime.modelId).toBe('gpt-4.1');
    expect(runtime.apiModel).toBe('gpt-4.1');
    expect(runtime.baseURL).toBe('https://api.openai.com/v1');
    expect(runtime.capabilities).toContain('tool-calling');
  });

  it('resolves profile provider metadata and keeps profile source', () => {
    const runtime = resolveZeroProviderRuntime({
      provider: 'anthropic',
      model: 'sonnet-4.5',
      profileName: 'work-claude',
      source: 'profile',
      baseURL: 'https://api.anthropic.com/',
    });

    expect(runtime.provider).toBe('anthropic');
    expect(runtime.profileName).toBe('work-claude');
    expect(runtime.modelId).toBe('claude-sonnet-4.5');
    expect(runtime.apiModel).toBe('claude-sonnet-4-5-20250929');
    expect(runtime.baseURL).toBe('https://api.anthropic.com');
  });

  it('rejects explicit provider mismatches for known models', () => {
    expect(() =>
      resolveZeroProviderRuntime({
        provider: 'google',
        model: 'gpt-4.1',
      })
    ).toThrow('belongs to openai');
  });

  it('allows custom OpenAI-compatible gateways to use registry models', () => {
    const runtime = resolveZeroProviderRuntime({
      provider: 'openai-compatible',
      model: 'gpt-4o',
      baseURL: 'http://localhost:11434/v1',
    });

    expect(runtime.provider).toBe('openai-compatible');
    expect(runtime.apiModel).toBe('gpt-4o');
    expect(runtime.baseURL).toBe('http://localhost:11434/v1');
  });

  it('normalizes official OpenAI shorthand URLs to the /v1 endpoint', () => {
    const runtime = resolveZeroProviderRuntime({
      model: 'gpt-4.1',
      baseURL: 'https://api.openai.com',
    });

    expect(runtime.provider).toBe('openai');
    expect(runtime.baseURL).toBe('https://api.openai.com/v1');
  });

  it('rejects OpenAI-compatible configs without a custom gateway base URL', () => {
    expect(() =>
      resolveZeroProviderRuntime({
        provider: 'openai-compatible',
        model: 'local-coder',
      })
    ).toThrow('requires an explicit non-official baseURL');

    expect(() =>
      resolveZeroProviderRuntime({
        provider: 'openai-compatible',
        model: 'local-coder',
        baseURL: 'https://api.openai.com/v1',
      })
    ).toThrow('requires an explicit non-official baseURL');
  });

  it('allows unknown models only for OpenAI-compatible gateways', () => {
    const runtime = resolveZeroProviderRuntime({
      provider: 'openai-compatible',
      model: 'local-coder:latest',
      baseURL: 'http://localhost:11434/v1',
    });

    expect(runtime.provider).toBe('openai-compatible');
    expect(runtime.modelId).toBeUndefined();
    expect(runtime.apiModel).toBe('local-coder:latest');
  });

  it('rejects unknown models for official provider runtimes', () => {
    expect(() =>
      resolveZeroProviderRuntime({
        provider: 'google',
        model: 'not-in-registry',
      })
    ).toThrow('Official providers require a model from the Zero model registry');
  });

  it('rejects unknown models without an OpenAI-compatible provider hint', () => {
    expect(() =>
      resolveZeroProviderRuntime({
        model: 'not-in-registry',
        baseURL: 'https://api.openai.com/v1',
      })
    ).toThrow('Unknown Zero model');
  });

  it('creates implemented OpenAI-compatible and Anthropic providers', () => {
    const openAI = resolveZeroProviderRuntime({
      provider: 'openai-compatible',
      model: 'local-coder',
      baseURL: 'http://localhost:11434/v1',
    });
    expect(createZeroProvider(openAI)).toBeDefined();

    const anthropic = resolveZeroProviderRuntime({
      model: 'sonnet-4.5',
      apiKey: 'test-anthropic-key',
    });
    const provider = createZeroProvider(anthropic);
    expect(provider).toBeDefined();
    expect((provider as any).maxTokens).toBe(64000);
  });

  it('requires an API key for the official Anthropic runtime', () => {
    const anthropic = resolveZeroProviderRuntime({
      model: 'sonnet-4.5',
    });

    expect(() => createZeroProvider(anthropic)).toThrow(
      'anthropic provider requires an API key'
    );
  });

  it('still rejects pending official Google adapters', () => {
    const google = resolveZeroProviderRuntime({
      model: 'gemini-flash',
      apiKey: 'test-google-key',
    });

    expect(() => createZeroProvider(google)).toThrow(ZeroPendingProviderError);
  });

  it('creates OpenAI-compatible providers without an API key for custom gateways', () => {
    const runtime = resolveZeroProviderRuntime({
      provider: 'openai-compatible',
      model: 'local-coder',
      baseURL: 'http://localhost:11434/v1',
    });

    expect(createZeroProvider(runtime)).toBeDefined();
  });

  it('requires an API key for the official OpenAI runtime', () => {
    const officialOpenAI = resolveZeroProviderRuntime({
      model: 'gpt-4.1',
    });

    expect(() => createZeroProvider(officialOpenAI)).toThrow(
      'openai provider requires an API key'
    );
  });
});
