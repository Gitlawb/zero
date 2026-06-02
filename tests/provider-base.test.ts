import { describe, it, expect } from 'bun:test';
import { seedMessages, collectStream } from '../src/providers/base';
import type { Provider, StreamEvent } from '../src/providers/types';

describe('seedMessages', () => {
  it('produces a system message followed by a user message', () => {
    const messages = seedMessages('you are a helper', 'hi');
    expect(messages).toHaveLength(2);
    expect(messages[0]).toEqual({ role: 'system', content: 'you are a helper' });
    expect(messages[1]).toEqual({ role: 'user', content: 'hi' });
  });
});

describe('collectStream', () => {
  it('collects text and tool calls from an async iterable', async () => {
    const events: StreamEvent[] = [
      { type: 'text', content: 'Hello ' },
      { type: 'text', content: 'world' },
      { type: 'tool-call-start', id: 'c1', name: 'read_file' },
      { type: 'tool-call-delta', id: 'c1', argumentsFragment: '{"pat' },
      { type: 'tool-call-delta', id: 'c1', argumentsFragment: 'h":"x"}' },
      { type: 'tool-call-end', id: 'c1' },
      { type: 'done' },
    ];

    async function* stream() {
      for (const e of events) yield e;
    }

    const result = await collectStream(stream());
    expect(result.text).toBe('Hello world');
    expect(result.toolCalls).toEqual([
      { id: 'c1', name: 'read_file', arguments: '{"path":"x"}' },
    ]);
  });
});

describe('Provider interface (contract)', () => {
  it('can be implemented by a mock that yields the expected events', async () => {
    const mock: Provider = {
      async *streamCompletion() {
        yield { type: 'text', content: 'ok' };
        yield { type: 'done' };
      },
    };

    const collected: string[] = [];
    for await (const event of mock.streamCompletion([], [])) {
      if (event.type === 'text') collected.push(event.content);
    }
    expect(collected.join('')).toBe('ok');
  });
});
