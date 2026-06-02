import { describe, expect, it } from 'bun:test';
import { OpenAIProvider } from '../src/providers/openai';
import type { StreamEvent } from '../src/providers/types';

function makeProviderWithChunks(chunks: any[]): OpenAIProvider {
  const provider = new OpenAIProvider({ apiKey: 'test', model: 'test' });
  (provider as any).client = {
    chat: {
      completions: {
        create: async function* () {
          for (const chunk of chunks) {
            yield chunk;
          }
        },
      },
    },
  };
  return provider;
}

describe('OpenAIProvider tool-call streaming', () => {
  it('buffers argument deltas until the real tool call id arrives', async () => {
    const provider = makeProviderWithChunks([
      {
        choices: [
          {
            delta: {
              tool_calls: [
                { index: 0, function: { arguments: '{"path"' } },
              ],
            },
          },
        ],
      },
      {
        choices: [
          {
            delta: {
              tool_calls: [
                { index: 0, id: 'call_1', function: { name: 'read_file' } },
              ],
            },
          },
        ],
      },
      {
        choices: [
          {
            delta: {
              tool_calls: [
                { index: 0, function: { arguments: ':"src/index.ts"}' } },
              ],
            },
            finish_reason: 'tool_calls',
          },
        ],
      },
    ]);

    const events: StreamEvent[] = [];
    for await (const event of provider.streamCompletion([], [])) {
      events.push(event);
    }

    const starts = events.filter(event => event.type === 'tool-call-start');
    const deltas = events.filter(event => event.type === 'tool-call-delta');

    expect(starts).toEqual([{ type: 'tool-call-start', id: 'call_1', name: 'read_file' }]);
    expect(deltas.every(event => event.id === 'call_1')).toBe(true);
    expect(deltas.map(event => event.argumentsFragment).join('')).toBe('{"path":"src/index.ts"}');
  });
});
