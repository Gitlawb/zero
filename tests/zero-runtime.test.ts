import { describe, expect, it } from 'bun:test';
import {
  parseToolList,
  parseZeroAutonomyLevel,
  resolvePermissionMode,
  resolveReasoningEffort,
  resolveRuntimeModel,
  ZeroRuntimeUsageError,
} from '../src/zero-runtime';
import { getZeroModel } from '../src/zero-model-registry';

describe('Zero runtime helpers', () => {
  it('parses autonomy and tool lists', () => {
    expect(parseZeroAutonomyLevel(undefined)).toBe('low');
    expect(parseZeroAutonomyLevel('HIGH')).toBe('high');
    expect(parseToolList('read_file,bash write_file')).toEqual([
      'read_file',
      'bash',
      'write_file',
    ]);
    expect(() => parseZeroAutonomyLevel('chaos')).toThrow(ZeroRuntimeUsageError);
  });

  it('maps surface and autonomy to permission mode', () => {
    expect(resolvePermissionMode({ surface: 'tui', autonomy: 'low' })).toBe('ask');
    expect(resolvePermissionMode({ surface: 'exec', autonomy: 'low' })).toBe('auto');
    expect(resolvePermissionMode({ surface: 'exec', autonomy: 'high' })).toBe('unsafe');
    expect(resolvePermissionMode({
      surface: 'exec',
      autonomy: 'low',
      skipPermissionsUnsafe: true,
    })).toBe('unsafe');
  });

  it('resolves model profiles before configured models', () => {
    expect(resolveRuntimeModel({
      configuredModel: 'gpt-4.1',
      modelProfile: 'deep',
    }).profile?.id).toBe('deep');

    expect(resolveRuntimeModel({
      configuredModel: 'gpt-4.1',
      model: 'fast',
    }).profile?.id).toBe('fast');
  });

  it('validates reasoning effort against the selected model', () => {
    const model = getZeroModel('claude-sonnet-4.5');

    expect(resolveReasoningEffort('high', model)).toBe('high');
    expect(resolveReasoningEffort('off')).toBe('none');
    expect(() => resolveReasoningEffort('off', model)).toThrow(ZeroRuntimeUsageError);
    expect(() => resolveReasoningEffort('xhigh', model)).toThrow(ZeroRuntimeUsageError);
    expect(() => resolveReasoningEffort('warp', model)).toThrow(ZeroRuntimeUsageError);
  });
});
