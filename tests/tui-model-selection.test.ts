import { describe, expect, it } from 'bun:test';
import {
  buildTuiModelStatus,
  formatModelListLines,
  formatModelProfileLines,
  getSelectableZeroModels,
  resolveTuiModelProfileSelection,
  resolveTuiModelSelection,
} from '../src/tui/model-selection';

describe('TUI model selection helpers', () => {
  it('lists active registry models in a stable provider order', () => {
    const models = getSelectableZeroModels();

    expect(models.length).toBeGreaterThan(0);
    expect(models.some((model) => model.status === 'deprecated')).toBe(false);
    expect(models[0]?.provider).toBe('openai');
    expect(models.map((model) => model.id)).toContain('claude-sonnet-4.5');
    expect(models.map((model) => model.id)).toContain('gemini-2.5-flash');
  });

  it('resolves model ids and aliases for slash command selection', () => {
    expect(resolveTuiModelSelection('sonnet-4.5')?.id).toBe('claude-sonnet-4.5');
    expect(resolveTuiModelSelection('google:gemini-2.5-flash')?.id).toBe('gemini-2.5-flash');
    expect(resolveTuiModelSelection('unknown-model')).toBeUndefined();
  });

  it('builds session override status without mutating provider config', () => {
    const status = buildTuiModelStatus(
      {
        model: 'gpt-4.1',
        provider: 'openai',
        source: 'profile',
        profileName: 'work',
      },
      'claude-sonnet-4.5'
    );

    expect(status.modelId).toBe('claude-sonnet-4.5');
    expect(status.label).toBe('Claude Sonnet 4.5');
    expect(status.providerLabel).toBe('anthropic');
    expect(status.sourceLabel).toBe('session');
  });

  it('formats compact model list lines for help output', () => {
    const lines = formatModelListLines(3);

    expect(lines).toHaveLength(4);
    expect(lines[0]).toContain('gpt');
    expect(lines[3]).toContain('more');
  });

  it('resolves intent-based model profiles', () => {
    const deep = resolveTuiModelProfileSelection('deep');

    expect(deep?.profile.id).toBe('deep');
    expect(deep?.model.status).not.toBe('deprecated');
    expect(resolveTuiModelSelection('cheap')).toBeDefined();
    expect(formatModelProfileLines().join('\n')).toContain('balanced');
  });
});
