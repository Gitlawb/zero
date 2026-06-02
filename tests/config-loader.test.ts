import { describe, it, expect } from 'bun:test';
import { mkdtempSync, rmSync, writeFileSync } from 'fs';
import { tmpdir } from 'os';
import { join } from 'path';
import { loadConfig, loadConfigWithLayers, mergeLayers, ZeroConfigSchema } from '../src/config/loader';
import { parseProviderProfileKind } from '../src/config/types';
import { ConfigManager } from '../src/config/manager';
import { ZERO_DEFAULT_MODEL_ID } from '../src/zero-model-registry';

function freshTmp(): string {
  return mkdtempSync(join(tmpdir(), 'zero-cfg-'));
}

describe('mergeLayers', () => {
  it('later layers override earlier ones', () => {
    const merged = mergeLayers(
      { providers: [], maxTurns: 12 },
      { providers: [] },
      { maxTurns: 5 },
    );
    expect(merged.maxTurns).toBe(5);
  });

  it('preserves providers from earlier layers when later ones are empty', () => {
    const merged = mergeLayers(
      { providers: [{ name: 'p1', baseURL: 'https://x.test', model: 'm' }] },
      { providers: [] },
    );
    expect(merged.providers).toHaveLength(1);
  });

  it('merges partial provider overrides by provider name', () => {
    const merged = mergeLayers(
      { providers: [{ name: 'p1', baseURL: 'https://x.test', model: 'old' }] },
      { providers: [{ name: 'p1', model: 'new' } as any] },
    );

    expect(merged.providers?.[0]).toEqual({
      name: 'p1',
      baseURL: 'https://x.test',
      model: 'new',
    });
  });
});

describe('loadConfigWithLayers', () => {
  it('returns built-in defaults when no other layer is present', () => {
    const tmp = freshTmp();
    try {
      const { effective, layers } = loadConfigWithLayers({
        userConfigPath: join(tmp, 'no-user.json'),
        projectConfigPath: join(tmp, 'no-project.json'),
        env: {},
      });
      expect(effective.maxTurns).toBe(12);
      expect(effective.planMode).toBe(false);
      expect(layers[0]?.source).toBe('defaults');
    } finally {
      rmSync(tmp, { recursive: true, force: true });
    }
  });

  it('reads a user config file', () => {
    const tmp = freshTmp();
    try {
      const user = join(tmp, 'user.json');
      writeFileSync(
        user,
        JSON.stringify({ maxTurns: 25, providers: [] }),
        'utf-8',
      );

      const { effective, layers } = loadConfigWithLayers({
        userConfigPath: user,
        projectConfigPath: join(tmp, 'no-project.json'),
        env: {},
      });

      expect(effective.maxTurns).toBe(25);
      const sources = layers.map((l) => l.source);
      expect(sources).toContain('user');
    } finally {
      rmSync(tmp, { recursive: true, force: true });
    }
  });

  it('applies env overrides (ZERO_MAX_TURNS, ZERO_DEBUG, ZERO_PLAN_MODE)', () => {
    const tmp = freshTmp();
    try {
      const { effective } = loadConfigWithLayers({
        userConfigPath: join(tmp, 'no-user.json'),
        projectConfigPath: join(tmp, 'no-project.json'),
        env: {
          ZERO_MAX_TURNS: '8',
          ZERO_DEBUG: 'true',
          ZERO_PLAN_MODE: '1',
        },
      });
      expect(effective.maxTurns).toBe(8);
      expect(effective.debug).toBe(true);
      expect(effective.planMode).toBe(true);
    } finally {
      rmSync(tmp, { recursive: true, force: true });
    }
  });

  it('lets CLI overrides win over everything else', () => {
    const tmp = freshTmp();
    try {
      const user = join(tmp, 'user.json');
      writeFileSync(user, JSON.stringify({ maxTurns: 25 }), 'utf-8');

      const { effective } = loadConfigWithLayers({
        userConfigPath: user,
        projectConfigPath: join(tmp, 'no-project.json'),
        env: { ZERO_MAX_TURNS: '8' },
        cliOverrides: { maxTurns: 3 },
      });

      expect(effective.maxTurns).toBe(3);
    } finally {
      rmSync(tmp, { recursive: true, force: true });
    }
  });

  it('tolerates malformed JSON in a config file', () => {
    const tmp = freshTmp();
    try {
      const user = join(tmp, 'user.json');
      writeFileSync(user, 'not json {', 'utf-8');

      // Should not throw; falls back to defaults
      const { effective } = loadConfigWithLayers({
        userConfigPath: user,
        projectConfigPath: join(tmp, 'no-project.json'),
        env: {},
      });

      expect(effective.maxTurns).toBe(12);
    } finally {
      rmSync(tmp, { recursive: true, force: true });
    }
  });
});

describe('ZeroConfigSchema', () => {
  it('rejects invalid provider URLs', () => {
    const result = ZeroConfigSchema.safeParse({
      providers: [{ name: 'p', baseURL: 'not a url', model: 'm' }],
    });
    expect(result.success).toBe(false);
  });

  it('accepts a minimal valid config', () => {
    const result = ZeroConfigSchema.safeParse({
      providers: [{
        name: 'p',
        provider: 'openai-compatible',
        baseURL: 'https://api.example.com',
        model: 'm',
      }],
    });
    expect(result.success).toBe(true);
  });

  it('rejects unknown provider kinds', () => {
    const result = ZeroConfigSchema.safeParse({
      providers: [{
        name: 'p',
        provider: 'made-up',
        baseURL: 'https://api.example.com',
        model: 'm',
      }],
    });
    expect(result.success).toBe(false);
  });

  it('parses provider kinds defensively for env and provider commands', () => {
    expect(parseProviderProfileKind('google')).toBe('google');
    expect(parseProviderProfileKind('OpenAI')).toBe('openai');
    expect(parseProviderProfileKind('GOOGLE')).toBe('google');
    expect(parseProviderProfileKind(undefined)).toBeUndefined();
    expect(() => parseProviderProfileKind('made-up')).toThrow(
      'Unknown Zero provider kind'
    );
  });
});

describe('ConfigManager', () => {
  it('uses OPENAI_API_KEY-only env config with the registry default model', () => {
    const manager = new ConfigManager({ providers: [] });
    const provider = manager.getEffectiveProviderConfig({
      OPENAI_API_KEY: 'sk-test',
    });

    expect(provider).toEqual({
      provider: undefined,
      baseURL: 'https://api.openai.com/v1',
      apiKey: 'sk-test',
      model: ZERO_DEFAULT_MODEL_ID,
      source: 'environment',
    });
  });

  it('returns active profile config with source metadata', () => {
    const manager = new ConfigManager({
      activeProvider: 'local',
      providers: [{
        name: 'local',
        provider: 'openai-compatible',
        baseURL: 'http://localhost:11434/v1',
        model: 'local-coder',
      }],
    });

    expect(manager.getEffectiveProviderConfig({})?.source).toBe('profile');
    expect(manager.getEffectiveProviderConfig({})?.profileName).toBe('local');
  });
});

describe('loadConfig', () => {
  it('returns the merged effective config (convenience wrapper)', () => {
    const tmp = freshTmp();
    try {
      const config = loadConfig({
        userConfigPath: join(tmp, 'no-user.json'),
        projectConfigPath: join(tmp, 'no-project.json'),
        env: { ZERO_MAX_TURNS: '7' },
      });
      expect(config.maxTurns).toBe(7);
    } finally {
      rmSync(tmp, { recursive: true, force: true });
    }
  });
});
