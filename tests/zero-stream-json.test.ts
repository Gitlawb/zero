import { describe, expect, it } from 'bun:test';
import {
  ZERO_STREAM_JSON_SCHEMA_VERSION,
  ZeroStreamJsonInputEventSchema,
  ZeroStreamJsonOutputEventSchema,
  formatZeroStreamJsonEvent,
  parseZeroStreamJsonInput,
  resolveZeroStreamJsonPrompt,
} from '../src/zero-stream-json';
import { ZERO_REDACTED_SECRET } from '../src/zero-redaction';

describe('Zero stream-json protocol', () => {
  it('validates representative output events against the frozen schema', () => {
    const runStart = ZeroStreamJsonOutputEventSchema.parse({
      schemaVersion: ZERO_STREAM_JSON_SCHEMA_VERSION,
      type: 'run_start',
      runId: 'run_20260603_abc123',
      sessionId: 'session_abc123',
      cwd: '/repo/zero',
      provider: 'openai',
      model: 'gpt-4.1',
      apiModel: 'gpt-4.1',
    });
    const text = ZeroStreamJsonOutputEventSchema.parse({
      schemaVersion: ZERO_STREAM_JSON_SCHEMA_VERSION,
      type: 'text',
      runId: 'run_20260603_abc123',
      delta: 'hello',
    });
    const toolCall = ZeroStreamJsonOutputEventSchema.parse({
      schemaVersion: ZERO_STREAM_JSON_SCHEMA_VERSION,
      type: 'tool_call',
      runId: 'run_20260603_abc123',
      id: 'call_1',
      name: 'read_file',
      args: { path: 'README.md' },
      sideEffect: 'read',
    });
    const runEnd = ZeroStreamJsonOutputEventSchema.parse({
      schemaVersion: ZERO_STREAM_JSON_SCHEMA_VERSION,
      type: 'run_end',
      runId: 'run_20260603_abc123',
      status: 'success',
      exitCode: 0,
    });

    expect(runStart).toMatchObject({ type: 'run_start' });
    expect(text).toMatchObject({ type: 'text', delta: 'hello' });
    expect(toolCall).toMatchObject({
      type: 'tool_call',
      args: { path: 'README.md' },
    });
    expect(runEnd).toMatchObject({ type: 'run_end', status: 'success' });
  });

  it('serializes one redacted JSON object per line', () => {
    const line = formatZeroStreamJsonEvent({
      schemaVersion: ZERO_STREAM_JSON_SCHEMA_VERSION,
      type: 'error',
      runId: 'run_secret',
      code: 'provider_error',
      message: 'api key sk-proj-abcdefghijklmnopqrstuvwxyz0123456789 leaked',
      recoverable: false,
    });

    expect(line).not.toContain('\n');
    expect(line).toContain(ZERO_REDACTED_SECRET);
    expect(line).not.toContain('sk-proj-abcdefghijklmnopqrstuvwxyz0123456789');
    expect(JSON.parse(line)).toMatchObject({
      schemaVersion: ZERO_STREAM_JSON_SCHEMA_VERSION,
      type: 'error',
      code: 'provider_error',
    });
  });

  it('parses input JSONL and resolves user prompt content', () => {
    const input = [
      JSON.stringify({
        schemaVersion: ZERO_STREAM_JSON_SCHEMA_VERSION,
        type: 'message',
        role: 'user',
        content: 'Inspect this repo.',
      }),
      JSON.stringify({
        schemaVersion: ZERO_STREAM_JSON_SCHEMA_VERSION,
        type: 'prompt',
        content: 'Focus on failing tests.',
      }),
      '',
    ].join('\n');

    const events = parseZeroStreamJsonInput(input);

    expect(events).toEqual([
      {
        schemaVersion: ZERO_STREAM_JSON_SCHEMA_VERSION,
        type: 'message',
        role: 'user',
        content: 'Inspect this repo.',
      },
      {
        schemaVersion: ZERO_STREAM_JSON_SCHEMA_VERSION,
        type: 'prompt',
        content: 'Focus on failing tests.',
      },
    ]);
    expect(resolveZeroStreamJsonPrompt(events)).toBe(
      'Inspect this repo.\n\nFocus on failing tests.'
    );
  });

  it('rejects malformed input with line numbers', () => {
    expect(() => parseZeroStreamJsonInput('{"type":"prompt"\n')).toThrow(
      'Invalid stream-json input at line 1'
    );

    expect(() =>
      parseZeroStreamJsonInput(
        JSON.stringify({
          schemaVersion: ZERO_STREAM_JSON_SCHEMA_VERSION,
          type: 'message',
          role: 'assistant',
          content: 'not accepted as input',
        })
      )
    ).toThrow('Invalid stream-json input at line 1');
  });

  it('keeps input schema strict enough for extension authors', () => {
    expect(() =>
      ZeroStreamJsonInputEventSchema.parse({
        schemaVersion: ZERO_STREAM_JSON_SCHEMA_VERSION,
        type: 'prompt',
        content: 'hello',
        unexpected: true,
      })
    ).toThrow();
  });
});
