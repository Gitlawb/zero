import type { Message, Provider, StreamEvent, ToolDefinition } from './types';

const DEFAULT_ANTHROPIC_BASE_URL = 'https://api.anthropic.com';
const DEFAULT_ANTHROPIC_VERSION = '2023-06-01';
const DEFAULT_MAX_TOKENS = 4096;

type FetchLike = (...args: Parameters<typeof fetch>) => ReturnType<typeof fetch>;

interface AnthropicProviderOptions {
  apiKey: string;
  baseURL?: string;
  model: string;
  maxTokens?: number;
  version?: string;
  beta?: string;
  fetchImpl?: FetchLike;
}

type AnthropicContentBlock =
  | { type: 'text'; text: string }
  | { type: 'tool_use'; id: string; name: string; input: Record<string, unknown> }
  | { type: 'tool_result'; tool_use_id: string; content: string };

interface AnthropicMessage {
  role: 'user' | 'assistant';
  content: string | AnthropicContentBlock[];
}

interface AnthropicStreamPayload {
  type?: string;
  index?: number;
  message?: {
    usage?: AnthropicUsage;
  };
  content_block?: {
    type?: string;
    id?: string;
    name?: string;
    input?: Record<string, unknown>;
  };
  delta?: {
    type?: string;
    text?: string;
    partial_json?: string;
    stop_reason?: string;
  };
  usage?: AnthropicUsage;
  error?: {
    type?: string;
    message?: string;
  };
}

interface AnthropicUsage {
  input_tokens?: number;
  output_tokens?: number;
}

interface AnthropicToolCallBlock {
  id: string;
  name: string;
}

export class AnthropicProvider implements Provider {
  private readonly apiKey: string;
  private readonly baseURL: string;
  private readonly model: string;
  private readonly maxTokens: number;
  private readonly version: string;
  private readonly beta?: string;
  private readonly fetchImpl: FetchLike;

  constructor({
    apiKey,
    baseURL,
    model,
    maxTokens = DEFAULT_MAX_TOKENS,
    version = DEFAULT_ANTHROPIC_VERSION,
    beta,
    fetchImpl = fetch,
  }: AnthropicProviderOptions) {
    this.apiKey = apiKey;
    this.baseURL = (baseURL || DEFAULT_ANTHROPIC_BASE_URL).replace(/\/+$/, '');
    this.model = model;
    this.maxTokens = normalizeMaxTokens(maxTokens);
    this.version = version;
    this.beta = beta;
    this.fetchImpl = fetchImpl;
  }

  async *streamCompletion(
    messages: Message[],
    tools: ToolDefinition[]
  ): AsyncIterable<StreamEvent> {
    const { system, messages: anthropicMessages } = toAnthropicMessages(messages);
    if (anthropicMessages.length === 0) {
      throw new Error('Zero Anthropic provider requires at least one non-system message');
    }

    const body: Record<string, unknown> = {
      model: this.model,
      max_tokens: this.maxTokens,
      messages: anthropicMessages,
      stream: true,
    };
    if (system) body.system = system;
    if (tools.length > 0) body.tools = toAnthropicTools(tools);

    const response = await this.createStream(body);
    yield* this.readStream(response);
  }

  private async createStream(body: Record<string, unknown>): Promise<Response> {
    const headers: Record<string, string> = {
      'content-type': 'application/json',
      'x-api-key': this.apiKey,
      'anthropic-version': this.version,
    };
    if (this.beta) headers['anthropic-beta'] = this.beta;

    let response: Response;
    try {
      response = await this.fetchImpl(`${this.baseURL}/v1/messages`, {
        method: 'POST',
        headers,
        body: JSON.stringify(body),
      });
    } catch (err: any) {
      throw new Error(`Provider returned error: ${getDetailedErrorMessage(err)}`);
    }

    if (!response.ok) {
      const message = await getResponseErrorMessage(response);
      if (response.status === 401 || response.status === 403) {
        throw new Error(`Provider authentication error (check your API key): ${message}`);
      }
      if (response.status === 429 || response.status === 529) {
        throw new Error(`Provider rate limit error: ${message}`);
      }
      throw new Error(`Provider returned error: ${message}`);
    }

    if (!response.body) {
      throw new Error('Provider returned error: Anthropic stream response did not include a body');
    }

    return response;
  }

  private async *readStream(response: Response): AsyncIterable<StreamEvent> {
    const toolBlocks = new Map<number, AnthropicToolCallBlock>();
    let promptTokens = 0;
    let completionTokens = 0;
    let hasPromptUsage = false;
    let hasCompletionUsage = false;
    let emittedDone = false;

    try {
      for await (const payload of readAnthropicSSE(response.body!)) {
        switch (payload.type) {
          case 'message_start': {
            const inputTokens = payload.message?.usage?.input_tokens;
            if (typeof inputTokens === 'number') {
              promptTokens = inputTokens;
              hasPromptUsage = true;
            }
            break;
          }
          case 'content_block_start': {
            if (
              typeof payload.index === 'number' &&
              payload.content_block?.type === 'tool_use' &&
              payload.content_block.id &&
              payload.content_block.name
            ) {
              const toolBlock = {
                id: payload.content_block.id,
                name: payload.content_block.name,
              };
              toolBlocks.set(payload.index, toolBlock);
              yield { type: 'tool-call-start', id: toolBlock.id, name: toolBlock.name };

              const initialInput = payload.content_block.input;
              if (initialInput && Object.keys(initialInput).length > 0) {
                yield {
                  type: 'tool-call-delta',
                  id: toolBlock.id,
                  argumentsFragment: JSON.stringify(initialInput),
                };
              }
            }
            break;
          }
          case 'content_block_delta': {
            const delta = payload.delta;
            if (delta?.type === 'text_delta' && delta.text) {
              yield { type: 'text', content: delta.text };
            } else if (
              delta?.type === 'input_json_delta' &&
              typeof payload.index === 'number'
            ) {
              const toolBlock = toolBlocks.get(payload.index);
              if (toolBlock && delta.partial_json) {
                yield {
                  type: 'tool-call-delta',
                  id: toolBlock.id,
                  argumentsFragment: delta.partial_json,
                };
              }
            }
            break;
          }
          case 'content_block_stop': {
            if (typeof payload.index === 'number') {
              const toolBlock = toolBlocks.get(payload.index);
              if (toolBlock) {
                yield { type: 'tool-call-end', id: toolBlock.id };
                toolBlocks.delete(payload.index);
              }
            }
            break;
          }
          case 'message_delta': {
            const inputTokens = payload.usage?.input_tokens;
            const outputTokens = payload.usage?.output_tokens;
            if (typeof inputTokens === 'number') {
              promptTokens = inputTokens;
              hasPromptUsage = true;
            }
            if (typeof outputTokens === 'number') {
              completionTokens = outputTokens;
              hasCompletionUsage = true;
            }
            break;
          }
          case 'message_stop': {
            for (const toolBlock of toolBlocks.values()) {
              yield { type: 'tool-call-end', id: toolBlock.id };
            }
            toolBlocks.clear();
            if (hasPromptUsage || hasCompletionUsage) {
              yield { type: 'usage', promptTokens, completionTokens };
            }
            yield { type: 'done' };
            emittedDone = true;
            break;
          }
          case 'error': {
            throw new Error(payload.error?.message || payload.error?.type || 'Anthropic stream error');
          }
          default:
            break;
        }
      }

      if (!emittedDone) {
        for (const toolBlock of toolBlocks.values()) {
          yield { type: 'tool-call-end', id: toolBlock.id };
        }
        if (hasPromptUsage || hasCompletionUsage) {
          yield { type: 'usage', promptTokens, completionTokens };
        }
        yield { type: 'done' };
      }
    } catch (err: any) {
      throw new Error(`Provider returned error during streaming: ${getDetailedErrorMessage(err)}`);
    }
  }
}

async function* readAnthropicSSE(
  body: ReadableStream<Uint8Array>
): AsyncIterable<AnthropicStreamPayload> {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';

  while (true) {
    const { value, done } = await reader.read();
    buffer += decoder.decode(value, { stream: !done });

    let boundary = findSSEBoundary(buffer);
    while (boundary) {
      const rawEvent = buffer.slice(0, boundary.index);
      buffer = buffer.slice(boundary.index + boundary.length);
      const payload = parseAnthropicSSEPayload(rawEvent);
      if (payload) yield payload;
      boundary = findSSEBoundary(buffer);
    }

    if (done) break;
  }

  const trailingPayload = parseAnthropicSSEPayload(buffer);
  if (trailingPayload) yield trailingPayload;
}

function findSSEBoundary(buffer: string): { index: number; length: number } | undefined {
  const lfIndex = buffer.indexOf('\n\n');
  const crlfIndex = buffer.indexOf('\r\n\r\n');
  if (lfIndex === -1 && crlfIndex === -1) return undefined;
  if (lfIndex === -1) return { index: crlfIndex, length: 4 };
  if (crlfIndex === -1 || lfIndex < crlfIndex) return { index: lfIndex, length: 2 };
  return { index: crlfIndex, length: 4 };
}

function parseAnthropicSSEPayload(rawEvent: string): AnthropicStreamPayload | undefined {
  const dataLines: string[] = [];

  for (const line of rawEvent.replace(/\r\n/g, '\n').split('\n')) {
    if (!line || line.startsWith(':')) continue;
    if (line.startsWith('data:')) {
      dataLines.push(line.slice('data:'.length).trimStart());
    }
  }

  if (dataLines.length === 0) return undefined;
  const data = dataLines.join('\n').trim();
  if (!data || data === '[DONE]') return undefined;

  try {
    return JSON.parse(data) as AnthropicStreamPayload;
  } catch (err: any) {
    throw new Error(`Invalid Anthropic stream payload: ${getDetailedErrorMessage(err)}`);
  }
}

function toAnthropicMessages(messages: Message[]): {
  system?: string;
  messages: AnthropicMessage[];
} {
  const systemParts: string[] = [];
  const anthropicMessages: AnthropicMessage[] = [];

  for (const message of messages) {
    const content = normalizeMessageContent(message.content);
    if (message.role === 'system') {
      if (content) systemParts.push(content);
      continue;
    }

    if (message.role === 'tool') {
      if (!message.toolCallId) {
        throw new Error('Zero Anthropic provider requires toolCallId on tool result messages');
      }
      appendUserBlocks(anthropicMessages, [
        {
          type: 'tool_result',
          tool_use_id: message.toolCallId,
          content,
        },
      ]);
      continue;
    }

    if (message.role === 'assistant') {
      const blocks: AnthropicContentBlock[] = [];
      if (content) blocks.push({ type: 'text', text: content });
      for (const toolCall of message.toolCalls ?? []) {
        blocks.push({
          type: 'tool_use',
          id: toolCall.id,
          name: toolCall.name,
          input: parseToolArguments(toolCall.arguments, toolCall.name),
        });
      }
      anthropicMessages.push({
        role: 'assistant',
        content: blocks.length === 1 && blocks[0]?.type === 'text'
          ? blocks[0].text
          : blocks,
      });
      continue;
    }

    appendUserText(anthropicMessages, content);
  }

  return {
    system: systemParts.length > 0 ? systemParts.join('\n\n') : undefined,
    messages: anthropicMessages,
  };
}

function appendUserText(messages: AnthropicMessage[], text: string): void {
  if (!text) return;
  appendUserBlocks(messages, [{ type: 'text', text }]);
}

function appendUserBlocks(
  messages: AnthropicMessage[],
  blocks: AnthropicContentBlock[]
): void {
  const last = messages.at(-1);
  if (last?.role === 'user') {
    last.content = [...toContentBlocks(last.content), ...blocks];
    return;
  }
  messages.push({ role: 'user', content: blocks });
}

function toContentBlocks(content: string | AnthropicContentBlock[]): AnthropicContentBlock[] {
  return typeof content === 'string' ? [{ type: 'text', text: content }] : content;
}

function toAnthropicTools(tools: ToolDefinition[]): Array<Record<string, unknown>> {
  return tools.map((tool) => ({
    name: tool.name,
    description: tool.description,
    input_schema: tool.parameters,
  }));
}

function parseToolArguments(argumentsJson: string, toolName: string): Record<string, unknown> {
  if (!argumentsJson) return {};
  try {
    const parsed = JSON.parse(argumentsJson);
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      return parsed as Record<string, unknown>;
    }
    throw new Error(
      `Zero Anthropic provider requires tool arguments for ${toolName} to be a JSON object`
    );
  } catch (err: any) {
    if (err?.message?.includes('JSON object')) throw err;
    throw new Error(
      `Zero Anthropic provider could not parse tool arguments for ${toolName} as JSON`
    );
  }
}

function normalizeMaxTokens(maxTokens: number): number {
  if (!Number.isFinite(maxTokens) || !Number.isInteger(maxTokens) || maxTokens < 1) {
    throw new Error('Zero Anthropic provider maxTokens must be a positive integer');
  }
  return maxTokens;
}

function normalizeMessageContent(content: unknown): string {
  if (typeof content === 'string') return content;
  if (content == null) return '';
  return String(content);
}

async function getResponseErrorMessage(response: Response): Promise<string> {
  const body = await response.text().catch(() => '');
  if (!body) return `${response.status} ${response.statusText}`.trim();

  try {
    const parsed = JSON.parse(body);
    return parsed.error?.message || parsed.message || body;
  } catch {
    return body;
  }
}

function getDetailedErrorMessage(err: any): string {
  if (!err) return 'Unknown error';
  if (err.message && !err.message.includes('Provider returned error')) return err.message;
  if (err.error?.message) return err.error.message;
  if (typeof err.error === 'string') return err.error;
  if (err.cause?.message) return err.cause.message;
  return err.message || String(err);
}
