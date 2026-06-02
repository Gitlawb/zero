import { describe, expect, it } from 'bun:test';
import { AnthropicProvider } from '../src/providers/anthropic';
import type { Message, StreamEvent, ToolDefinition } from '../src/providers/types';

describe('AnthropicProvider', () => {
  it('maps Zero messages, system prompts, and tools to the Anthropic Messages API', async () => {
    let capturedUrl = '';
    let capturedBody: any;
    let capturedHeaders: Headers | undefined;

    const provider = new AnthropicProvider({
      apiKey: 'test-anthropic-key',
      model: 'claude-sonnet-4-5-20250929',
      maxTokens: 64000,
      fetchImpl: async (url, init) => {
        capturedUrl = String(url);
        capturedBody = JSON.parse(String(init?.body));
        capturedHeaders = new Headers(init?.headers);
        return streamResponse([
          event('message_start', {
            type: 'message_start',
            message: { usage: { input_tokens: 3, output_tokens: 1 } },
          }),
          event('message_stop', { type: 'message_stop' }),
        ]);
      },
    });

    const messages: Message[] = [
      { role: 'system', content: 'You are Zero.' },
      { role: 'user', content: 'Read the file.' },
      {
        role: 'assistant',
        content: 'I will inspect it.',
        toolCalls: [
          {
            id: 'toolu_1',
            name: 'read_file',
            arguments: '{"path":"src/index.ts"}',
          },
        ],
      },
      { role: 'tool', toolCallId: 'toolu_1', content: 'file contents' },
    ];
    const tools: ToolDefinition[] = [
      {
        name: 'read_file',
        description: 'Read a file',
        parameters: {
          type: 'object',
          properties: { path: { type: 'string' } },
          required: ['path'],
        },
      },
    ];

    await collectEvents(provider.streamCompletion(messages, tools));

    expect(capturedUrl).toBe('https://api.anthropic.com/v1/messages');
    expect(capturedHeaders?.get('x-api-key')).toBe('test-anthropic-key');
    expect(capturedHeaders?.get('anthropic-version')).toBe('2023-06-01');
    expect(capturedBody).toEqual({
      model: 'claude-sonnet-4-5-20250929',
      max_tokens: 64000,
      stream: true,
      system: 'You are Zero.',
      messages: [
        {
          role: 'user',
          content: [{ type: 'text', text: 'Read the file.' }],
        },
        {
          role: 'assistant',
          content: [
            { type: 'text', text: 'I will inspect it.' },
            {
              type: 'tool_use',
              id: 'toolu_1',
              name: 'read_file',
              input: { path: 'src/index.ts' },
            },
          ],
        },
        {
          role: 'user',
          content: [
            {
              type: 'tool_result',
              tool_use_id: 'toolu_1',
              content: 'file contents',
            },
          ],
        },
      ],
      tools: [
        {
          name: 'read_file',
          description: 'Read a file',
          input_schema: {
            type: 'object',
            properties: { path: { type: 'string' } },
            required: ['path'],
          },
        },
      ],
    });
  });

  it('normalizes Anthropic text and usage stream events', async () => {
    const provider = new AnthropicProvider({
      apiKey: 'test-key',
      model: 'claude-sonnet-4-5-20250929',
      fetchImpl: createFetch([
        event('message_start', {
          type: 'message_start',
          message: { usage: { input_tokens: 25, output_tokens: 1 } },
        }),
        event('content_block_start', {
          type: 'content_block_start',
          index: 0,
          content_block: { type: 'text', text: '' },
        }),
        event('content_block_delta', {
          type: 'content_block_delta',
          index: 0,
          delta: { type: 'text_delta', text: 'Hello' },
        }),
        event('content_block_delta', {
          type: 'content_block_delta',
          index: 0,
          delta: { type: 'text_delta', text: ' Zero' },
        }),
        event('message_delta', {
          type: 'message_delta',
          delta: { stop_reason: 'end_turn' },
          usage: { output_tokens: 15 },
        }),
        event('message_stop', { type: 'message_stop' }),
      ]),
    });

    const events = await collectEvents(provider.streamCompletion(
      [{ role: 'user', content: 'hello' }],
      []
    ));

    expect(events).toEqual([
      { type: 'text', content: 'Hello' },
      { type: 'text', content: ' Zero' },
      { type: 'usage', promptTokens: 25, completionTokens: 15 },
      { type: 'done' },
    ]);
  });

  it('normalizes Anthropic tool-use blocks and partial JSON input deltas', async () => {
    const provider = new AnthropicProvider({
      apiKey: 'test-key',
      model: 'claude-sonnet-4-5-20250929',
      fetchImpl: createFetch([
        event('message_start', {
          type: 'message_start',
          message: { usage: { input_tokens: 30, output_tokens: 2 } },
        }),
        event('content_block_start', {
          type: 'content_block_start',
          index: 1,
          content_block: {
            type: 'tool_use',
            id: 'toolu_1',
            name: 'read_file',
            input: {},
          },
        }),
        event('content_block_delta', {
          type: 'content_block_delta',
          index: 1,
          delta: { type: 'input_json_delta', partial_json: '{"path":' },
        }),
        event('content_block_delta', {
          type: 'content_block_delta',
          index: 1,
          delta: { type: 'input_json_delta', partial_json: '"src/index.ts"}' },
        }),
        event('content_block_stop', { type: 'content_block_stop', index: 1 }),
        event('message_delta', {
          type: 'message_delta',
          delta: { stop_reason: 'tool_use' },
          usage: { output_tokens: 42 },
        }),
        event('message_stop', { type: 'message_stop' }),
      ]),
    });

    const events = await collectEvents(provider.streamCompletion(
      [{ role: 'user', content: 'read file' }],
      []
    ));

    expect(events).toEqual([
      { type: 'tool-call-start', id: 'toolu_1', name: 'read_file' },
      { type: 'tool-call-delta', id: 'toolu_1', argumentsFragment: '{"path":' },
      { type: 'tool-call-delta', id: 'toolu_1', argumentsFragment: '"src/index.ts"}' },
      { type: 'tool-call-end', id: 'toolu_1' },
      { type: 'usage', promptTokens: 30, completionTokens: 42 },
      { type: 'done' },
    ]);
  });

  it('normalizes multiple Anthropic tool-use blocks in the same response', async () => {
    const provider = new AnthropicProvider({
      apiKey: 'test-key',
      model: 'claude-sonnet-4-5-20250929',
      fetchImpl: createFetch([
        event('content_block_start', {
          type: 'content_block_start',
          index: 0,
          content_block: { type: 'tool_use', id: 'toolu_1', name: 'read_file', input: {} },
        }),
        event('content_block_start', {
          type: 'content_block_start',
          index: 1,
          content_block: { type: 'tool_use', id: 'toolu_2', name: 'grep', input: {} },
        }),
        event('content_block_delta', {
          type: 'content_block_delta',
          index: 0,
          delta: { type: 'input_json_delta', partial_json: '{"path":"a.ts"}' },
        }),
        event('content_block_delta', {
          type: 'content_block_delta',
          index: 1,
          delta: { type: 'input_json_delta', partial_json: '{"pattern":"Zero"}' },
        }),
        event('content_block_stop', { type: 'content_block_stop', index: 0 }),
        event('content_block_stop', { type: 'content_block_stop', index: 1 }),
        event('message_stop', { type: 'message_stop' }),
      ]),
    });

    const events = await collectEvents(provider.streamCompletion(
      [{ role: 'user', content: 'read and grep' }],
      []
    ));

    expect(events).toEqual([
      { type: 'tool-call-start', id: 'toolu_1', name: 'read_file' },
      { type: 'tool-call-start', id: 'toolu_2', name: 'grep' },
      { type: 'tool-call-delta', id: 'toolu_1', argumentsFragment: '{"path":"a.ts"}' },
      { type: 'tool-call-delta', id: 'toolu_2', argumentsFragment: '{"pattern":"Zero"}' },
      { type: 'tool-call-end', id: 'toolu_1' },
      { type: 'tool-call-end', id: 'toolu_2' },
      { type: 'done' },
    ]);
  });

  it('surfaces Anthropic HTTP authentication errors with provider context', async () => {
    const provider = new AnthropicProvider({
      apiKey: 'bad-key',
      model: 'claude-sonnet-4-5-20250929',
      fetchImpl: async () =>
        new Response(JSON.stringify({ error: { message: 'invalid x-api-key' } }), {
          status: 401,
        }),
    });

    await expect(collectEvents(provider.streamCompletion(
      [{ role: 'user', content: 'hello' }],
      []
    ))).rejects.toThrow('Provider authentication error');
  });

  it('surfaces Anthropic HTTP rate limit errors with provider context', async () => {
    const provider = new AnthropicProvider({
      apiKey: 'test-key',
      model: 'claude-sonnet-4-5-20250929',
      fetchImpl: async () =>
        new Response(JSON.stringify({ error: { message: 'rate limited' } }), {
          status: 429,
        }),
    });

    await expect(collectEvents(provider.streamCompletion(
      [{ role: 'user', content: 'hello' }],
      []
    ))).rejects.toThrow('Provider rate limit error');
  });

  it('surfaces Anthropic stream error events with provider context', async () => {
    const provider = new AnthropicProvider({
      apiKey: 'test-key',
      model: 'claude-sonnet-4-5-20250929',
      fetchImpl: createFetch([
        event('error', {
          type: 'error',
          error: { type: 'overloaded_error', message: 'server overloaded' },
        }),
      ]),
    });

    await expect(collectEvents(provider.streamCompletion(
      [{ role: 'user', content: 'hello' }],
      []
    ))).rejects.toThrow('server overloaded');
  });

  it('rejects Anthropic calls without a non-system message', async () => {
    const provider = new AnthropicProvider({
      apiKey: 'test-key',
      model: 'claude-sonnet-4-5-20250929',
      fetchImpl: createFetch([]),
    });

    await expect(collectEvents(provider.streamCompletion(
      [{ role: 'system', content: 'Only system.' }],
      []
    ))).rejects.toThrow('requires at least one non-system message');
  });

  it('rejects tool results without a toolCallId before dispatch', async () => {
    const provider = new AnthropicProvider({
      apiKey: 'test-key',
      model: 'claude-sonnet-4-5-20250929',
      fetchImpl: createFetch([]),
    });

    await expect(collectEvents(provider.streamCompletion(
      [{ role: 'tool', content: 'missing id' }],
      []
    ))).rejects.toThrow('requires toolCallId');
  });

  it('rejects assistant tool calls whose arguments are not JSON objects', async () => {
    const provider = new AnthropicProvider({
      apiKey: 'test-key',
      model: 'claude-sonnet-4-5-20250929',
      fetchImpl: createFetch([]),
    });

    await expect(collectEvents(provider.streamCompletion(
      [
        { role: 'user', content: 'call tool' },
        {
          role: 'assistant',
          content: '',
          toolCalls: [{ id: 'toolu_1', name: 'read_file', arguments: '"src/index.ts"' }],
        },
      ],
      []
    ))).rejects.toThrow('requires tool arguments for read_file to be a JSON object');
  });

  it('validates maxTokens before dispatch', async () => {
    expect(() => new AnthropicProvider({
      apiKey: 'test-key',
      model: 'claude-sonnet-4-5-20250929',
      maxTokens: 0,
    })).toThrow('maxTokens must be a positive integer');
  });
});

function createFetch(events: string[]) {
  return async () => streamResponse(events);
}

function streamResponse(events: string[]): Response {
  return new Response(events.join(''), {
    headers: { 'content-type': 'text/event-stream' },
  });
}

function event(name: string, data: Record<string, unknown>): string {
  return `event: ${name}\ndata: ${JSON.stringify(data)}\n\n`;
}

async function collectEvents(stream: AsyncIterable<StreamEvent>): Promise<StreamEvent[]> {
  const events: StreamEvent[] = [];
  for await (const event of stream) events.push(event);
  return events;
}
