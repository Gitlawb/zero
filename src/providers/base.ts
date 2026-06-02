import type { Message, StreamEvent, Provider, ToolDefinition } from './types';

/**
 * Common helpers for provider implementations.
 *
 * The `Provider` interface itself lives in `./types.ts` so it can be
 * referenced by both runtime code (implementations) and test code
 * (mocks) without pulling in any concrete provider.
 */
export type { Message, StreamEvent, Provider, ToolDefinition } from './types';

/**
 * Build a fresh conversation seed with a system prompt and a single
 * user turn. Most providers expect this exact layout.
 */
export function seedMessages(systemPrompt: string, userPrompt: string): Message[] {
  return [
    { role: 'system', content: systemPrompt },
    { role: 'user', content: userPrompt },
  ];
}

/**
 * Drain an async iterable of `StreamEvent`s into a structured result.
 * Useful in tests and for any non-streaming consumer.
 */
export interface CollectedStream {
  text: string;
  toolCalls: Array<{ id: string; name: string; arguments: string }>;
}

export async function collectStream(stream: AsyncIterable<StreamEvent>): Promise<CollectedStream> {
  let text = '';
  const toolCalls: CollectedStream['toolCalls'] = [];
  const buffer = new Map<string, { id: string; name: string; arguments: string }>();

  for await (const event of stream) {
    if (event.type === 'text') {
      text += event.content;
    } else if (event.type === 'tool-call-start') {
      buffer.set(event.id, { id: event.id, name: event.name, arguments: '' });
    } else if (event.type === 'tool-call-delta') {
      const existing = buffer.get(event.id);
      if (existing) existing.arguments += event.argumentsFragment;
    } else if (event.type === 'tool-call-end') {
      const done = buffer.get(event.id);
      if (done) toolCalls.push(done);
    }
  }

  return { text, toolCalls };
}
