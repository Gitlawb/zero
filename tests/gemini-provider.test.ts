import { describe, expect, it } from 'bun:test';
import { GeminiProvider } from '../src/providers/gemini';
import type { Message, StreamEvent, ToolDefinition } from '../src/providers/types';

describe('GeminiProvider', () => {
  it('maps Zero messages, system prompts, and tools to Gemini streamGenerateContent', async () => {
    let capturedUrl = '';
    let capturedBody: any;
    let capturedHeaders: Headers | undefined;

    const provider = new GeminiProvider({
      apiKey: 'test-google-key',
      model: 'gemini-2.5-flash',
      maxTokens: 65536,
      fetchImpl: async (url, init) => {
        capturedUrl = String(url);
        capturedBody = JSON.parse(String(init?.body));
        capturedHeaders = new Headers(init?.headers);
        return streamResponse([
          event({
            usageMetadata: {
              promptTokenCount: 4,
              candidatesTokenCount: 2,
            },
          }),
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
            id: 'call_1',
            name: 'read_file',
            arguments: '{"path":"src/index.ts"}',
          },
        ],
      },
      { role: 'tool', toolCallId: 'call_1', content: 'file contents' },
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

    expect(capturedUrl).toBe(
      'https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:streamGenerateContent?alt=sse'
    );
    expect(capturedHeaders?.get('x-goog-api-key')).toBe('test-google-key');
    expect(capturedBody).toEqual({
      systemInstruction: {
        role: 'system',
        parts: [{ text: 'You are Zero.' }],
      },
      contents: [
        {
          role: 'user',
          parts: [{ text: 'Read the file.' }],
        },
        {
          role: 'model',
          parts: [
            { text: 'I will inspect it.' },
            {
              functionCall: {
                id: 'call_1',
                name: 'read_file',
                args: { path: 'src/index.ts' },
              },
            },
          ],
        },
        {
          role: 'user',
          parts: [
            {
              functionResponse: {
                id: 'call_1',
                name: 'read_file',
                response: { result: 'file contents' },
              },
            },
          ],
        },
      ],
      generationConfig: { maxOutputTokens: 65536 },
      tools: [
        {
          functionDeclarations: [
            {
              name: 'read_file',
              description: 'Read a file',
              parameters: {
                type: 'object',
                properties: { path: { type: 'string' } },
                required: ['path'],
              },
            },
          ],
        },
      ],
    });
  });

  it('merges user text after a tool result into the same Gemini user turn', async () => {
    let capturedBody: any;

    const provider = new GeminiProvider({
      apiKey: 'test-google-key',
      model: 'gemini-2.5-flash',
      fetchImpl: async (_url, init) => {
        capturedBody = JSON.parse(String(init?.body));
        return streamResponse([event({})]);
      },
    });

    await collectEvents(provider.streamCompletion(
      [
        { role: 'user', content: 'Read the file.' },
        {
          role: 'assistant',
          content: '',
          toolCalls: [
            {
              id: 'call_1',
              name: 'read_file',
              arguments: '{"path":"src/index.ts"}',
            },
          ],
        },
        { role: 'tool', toolCallId: 'call_1', content: 'file contents' },
        { role: 'user', content: 'Now grep for Zero.' },
      ],
      []
    ));

    expect(capturedBody.contents).toEqual([
      {
        role: 'user',
        parts: [{ text: 'Read the file.' }],
      },
      {
        role: 'model',
        parts: [
          {
            functionCall: {
              id: 'call_1',
              name: 'read_file',
              args: { path: 'src/index.ts' },
            },
          },
        ],
      },
      {
        role: 'user',
        parts: [
          {
            functionResponse: {
              id: 'call_1',
              name: 'read_file',
              response: { result: 'file contents' },
            },
          },
          { text: 'Now grep for Zero.' },
        ],
      },
    ]);
  });

  it('normalizes Gemini text, usage, and thinking token stream chunks', async () => {
    const provider = new GeminiProvider({
      apiKey: 'test-key',
      model: 'gemini-2.5-flash',
      fetchImpl: createFetch([
        event({
          candidates: [
            {
              content: {
                role: 'model',
                parts: [{ text: 'Hello' }],
              },
            },
          ],
        }),
        event({
          candidates: [
            {
              content: {
                role: 'model',
                parts: [{ text: ' Zero' }],
              },
            },
          ],
          usageMetadata: {
            promptTokenCount: 25,
            candidatesTokenCount: 15,
            thoughtsTokenCount: 3,
          },
        }),
      ]),
    });

    const events = await collectEvents(provider.streamCompletion(
      [{ role: 'user', content: 'hello' }],
      []
    ));

    expect(events).toEqual([
      { type: 'text', content: 'Hello' },
      { type: 'text', content: ' Zero' },
      { type: 'usage', promptTokens: 25, completionTokens: 18 },
      { type: 'done' },
    ]);
  });

  it('normalizes Gemini function calls from candidate parts', async () => {
    const provider = new GeminiProvider({
      apiKey: 'test-key',
      model: 'gemini-2.5-flash',
      fetchImpl: createFetch([
        event({
          candidates: [
            {
              content: {
                role: 'model',
                parts: [
                  {
                    functionCall: {
                      id: 'call_1',
                      name: 'read_file',
                      args: { path: 'src/index.ts' },
                    },
                  },
                  {
                    functionCall: {
                      id: 'call_2',
                      name: 'grep',
                      args: { pattern: 'Zero' },
                    },
                  },
                ],
              },
              finishReason: 'STOP',
            },
          ],
        }),
      ]),
    });

    const events = await collectEvents(provider.streamCompletion(
      [{ role: 'user', content: 'read and grep' }],
      []
    ));

    expect(events).toEqual([
      { type: 'tool-call-start', id: 'call_1', name: 'read_file' },
      { type: 'tool-call-delta', id: 'call_1', argumentsFragment: '{"path":"src/index.ts"}' },
      { type: 'tool-call-end', id: 'call_1' },
      { type: 'tool-call-start', id: 'call_2', name: 'grep' },
      { type: 'tool-call-delta', id: 'call_2', argumentsFragment: '{"pattern":"Zero"}' },
      { type: 'tool-call-end', id: 'call_2' },
      { type: 'done' },
    ]);
  });

  it('normalizes Gemini top-level functionCalls arrays', async () => {
    const provider = new GeminiProvider({
      apiKey: 'test-key',
      model: 'gemini-2.5-flash',
      fetchImpl: createFetch([
        event({
          functionCalls: [
            {
              id: 'call_1',
              name: 'read_file',
              args: { path: 'src/index.ts' },
            },
          ],
        }),
      ]),
    });

    const events = await collectEvents(provider.streamCompletion(
      [{ role: 'user', content: 'read file' }],
      []
    ));

    expect(events).toEqual([
      { type: 'tool-call-start', id: 'call_1', name: 'read_file' },
      { type: 'tool-call-delta', id: 'call_1', argumentsFragment: '{"path":"src/index.ts"}' },
      { type: 'tool-call-end', id: 'call_1' },
      { type: 'done' },
    ]);
  });

  it('surfaces Gemini HTTP authentication and rate limit errors with provider context', async () => {
    const authProvider = new GeminiProvider({
      apiKey: 'bad-key',
      model: 'gemini-2.5-flash',
      fetchImpl: async () =>
        new Response(JSON.stringify({ error: { message: 'API key not valid' } }), {
          status: 401,
        }),
    });

    await expect(collectEvents(authProvider.streamCompletion(
      [{ role: 'user', content: 'hello' }],
      []
    ))).rejects.toThrow('Provider authentication error');

    const rateProvider = new GeminiProvider({
      apiKey: 'test-key',
      model: 'gemini-2.5-flash',
      fetchImpl: async () =>
        new Response(JSON.stringify({ error: { message: 'quota exceeded' } }), {
          status: 429,
        }),
    });

    await expect(collectEvents(rateProvider.streamCompletion(
      [{ role: 'user', content: 'hello' }],
      []
    ))).rejects.toThrow('Provider rate limit error');
  });

  it('surfaces Gemini prompt block feedback with provider context', async () => {
    const provider = new GeminiProvider({
      apiKey: 'test-key',
      model: 'gemini-2.5-flash',
      fetchImpl: createFetch([
        event({
          promptFeedback: {
            blockReason: 'SAFETY',
            blockReasonMessage: 'blocked by policy',
          },
        }),
      ]),
    });

    await expect(collectEvents(provider.streamCompletion(
      [{ role: 'user', content: 'blocked prompt' }],
      []
    ))).rejects.toThrow('Content blocked: blocked by policy');
  });

  it('rejects Gemini calls without a non-system message', async () => {
    const provider = new GeminiProvider({
      apiKey: 'test-key',
      model: 'gemini-2.5-flash',
      fetchImpl: createFetch([]),
    });

    await expect(collectEvents(provider.streamCompletion(
      [{ role: 'system', content: 'Only system.' }],
      []
    ))).rejects.toThrow('requires at least one non-system message');
  });

  it('rejects malformed tool history before dispatch', async () => {
    const provider = new GeminiProvider({
      apiKey: 'test-key',
      model: 'gemini-2.5-flash',
      fetchImpl: createFetch([]),
    });

    await expect(collectEvents(provider.streamCompletion(
      [{ role: 'tool', content: 'missing id' }],
      []
    ))).rejects.toThrow('requires toolCallId');

    await expect(collectEvents(provider.streamCompletion(
      [
        { role: 'user', content: 'call tool' },
        {
          role: 'assistant',
          content: '',
          toolCalls: [{ id: 'call_1', name: 'read_file', arguments: '"src/index.ts"' }],
        },
      ],
      []
    ))).rejects.toThrow('requires tool arguments for read_file to be a JSON object');
  });

  it('validates maxTokens before dispatch', async () => {
    expect(() => new GeminiProvider({
      apiKey: 'test-key',
      model: 'gemini-2.5-flash',
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

function event(data: Record<string, unknown>): string {
  return `data: ${JSON.stringify(data)}\n\n`;
}

async function collectEvents(stream: AsyncIterable<StreamEvent>): Promise<StreamEvent[]> {
  const events: StreamEvent[] = [];
  for await (const event of stream) events.push(event);
  return events;
}
