import type { Message, Provider, StreamEvent, ToolDefinition } from './types';

const DEFAULT_GEMINI_BASE_URL = 'https://generativelanguage.googleapis.com';
const DEFAULT_MAX_TOKENS = 8192;

type FetchLike = (...args: Parameters<typeof fetch>) => ReturnType<typeof fetch>;

interface GeminiProviderOptions {
  apiKey: string;
  baseURL?: string;
  model: string;
  maxTokens?: number;
  fetchImpl?: FetchLike;
}

type GeminiPart =
  | { text: string }
  | { functionCall: GeminiFunctionCall }
  | { functionResponse: GeminiFunctionResponse };

interface GeminiContent {
  role: 'user' | 'model' | 'system';
  parts: GeminiPart[];
}

interface GeminiFunctionCall {
  id?: string;
  name: string;
  args?: Record<string, unknown>;
}

interface GeminiFunctionResponse {
  id?: string;
  name: string;
  response: { result: string };
}

interface GeminiStreamPayload {
  candidates?: Array<{
    content?: {
      parts?: Array<{
        text?: string;
        functionCall?: Partial<GeminiFunctionCall> & {
          args?: unknown;
          arguments?: unknown;
        };
      }>;
    };
    finishReason?: string;
  }>;
  functionCalls?: Array<Partial<GeminiFunctionCall> & {
    args?: unknown;
    arguments?: unknown;
  }>;
  promptFeedback?: {
    blockReason?: string;
    blockReasonMessage?: string;
  };
  usageMetadata?: GeminiUsageMetadata;
  error?: {
    code?: number;
    message?: string;
    status?: string;
  };
}

interface GeminiUsageMetadata {
  promptTokenCount?: number;
  candidatesTokenCount?: number;
  thoughtsTokenCount?: number;
  totalTokenCount?: number;
}

export class GeminiProvider implements Provider {
  private readonly apiKey: string;
  private readonly baseURL: string;
  private readonly model: string;
  private readonly maxTokens: number;
  private readonly fetchImpl: FetchLike;

  constructor({
    apiKey,
    baseURL,
    model,
    maxTokens = DEFAULT_MAX_TOKENS,
    fetchImpl = fetch,
  }: GeminiProviderOptions) {
    this.apiKey = apiKey;
    this.baseURL = (baseURL || DEFAULT_GEMINI_BASE_URL).replace(/\/+$/, '');
    this.model = normalizeModel(model);
    this.maxTokens = normalizeMaxTokens(maxTokens);
    this.fetchImpl = fetchImpl;
  }

  async *streamCompletion(
    messages: Message[],
    tools: ToolDefinition[]
  ): AsyncIterable<StreamEvent> {
    const { systemInstruction, contents } = toGeminiContents(messages);
    if (contents.length === 0) {
      throw new Error('Zero Gemini provider requires at least one non-system message');
    }

    const body: Record<string, unknown> = {
      contents,
      generationConfig: {
        maxOutputTokens: this.maxTokens,
      },
    };
    if (systemInstruction) body.systemInstruction = systemInstruction;
    if (tools.length > 0) body.tools = toGeminiTools(tools);

    const response = await this.createStream(body);
    yield* this.readStream(response);
  }

  private async createStream(body: Record<string, unknown>): Promise<Response> {
    let response: Response;
    try {
      response = await this.fetchImpl(this.streamURL(), {
        method: 'POST',
        headers: {
          'content-type': 'application/json',
          'x-goog-api-key': this.apiKey,
        },
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
      if (response.status === 429) {
        throw new Error(`Provider rate limit error: ${message}`);
      }
      throw new Error(`Provider returned error: ${message}`);
    }

    if (!response.body) {
      throw new Error('Provider returned error: Gemini stream response did not include a body');
    }

    return response;
  }

  private async *readStream(response: Response): AsyncIterable<StreamEvent> {
    let promptTokens = 0;
    let completionTokens = 0;
    let hasUsage = false;
    let syntheticToolCallIndex = 0;

    try {
      for await (const payload of readGeminiSSE(response.body!)) {
        if (payload.error) {
          throw new Error(payload.error.message || payload.error.status || 'Gemini stream error');
        }

        const blockReason = payload.promptFeedback?.blockReason;
        if (blockReason) {
          const message = payload.promptFeedback?.blockReasonMessage || blockReason;
          throw new Error(`Content blocked: ${message}`);
        }

        const usage = payload.usageMetadata;
        if (usage) {
          if (typeof usage.promptTokenCount === 'number') {
            promptTokens = usage.promptTokenCount;
          }
          const candidateTokens = typeof usage.candidatesTokenCount === 'number'
            ? usage.candidatesTokenCount
            : 0;
          const thoughtTokens = typeof usage.thoughtsTokenCount === 'number'
            ? usage.thoughtsTokenCount
            : 0;
          completionTokens = candidateTokens + thoughtTokens;
          hasUsage = true;
        }

        for (const candidate of payload.candidates ?? []) {
          for (const part of candidate.content?.parts ?? []) {
            if (part.text) {
              yield { type: 'text', content: part.text };
            }
            if (part.functionCall?.name) {
              syntheticToolCallIndex += 1;
              yield* emitGeminiToolCall(part.functionCall, syntheticToolCallIndex);
            }
          }
        }

        for (const functionCall of payload.functionCalls ?? []) {
          if (!functionCall.name) continue;
          syntheticToolCallIndex += 1;
          yield* emitGeminiToolCall(functionCall, syntheticToolCallIndex);
        }
      }

      if (hasUsage) {
        yield { type: 'usage', promptTokens, completionTokens };
      }
      yield { type: 'done' };
    } catch (err: any) {
      throw new Error(`Provider returned error during streaming: ${getDetailedErrorMessage(err)}`);
    }
  }

  private streamURL(): string {
    return `${this.baseURL}/v1beta/models/${encodeURIComponent(this.model)}:streamGenerateContent?alt=sse`;
  }
}

function* emitGeminiToolCall(
  functionCall: Partial<GeminiFunctionCall> & { args?: unknown; arguments?: unknown },
  syntheticIndex: number
): Iterable<StreamEvent> {
  if (!functionCall.name) return;
  const id = functionCall.id || `gemini_tool_${syntheticIndex}`;
  const args = normalizeFunctionCallArgs(
    functionCall.args ?? functionCall.arguments,
    functionCall.name
  );

  yield { type: 'tool-call-start', id, name: functionCall.name };
  yield {
    type: 'tool-call-delta',
    id,
    argumentsFragment: JSON.stringify(args),
  };
  yield { type: 'tool-call-end', id };
}

async function* readGeminiSSE(
  body: ReadableStream<Uint8Array>
): AsyncIterable<GeminiStreamPayload> {
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
      const payload = parseGeminiSSEPayload(rawEvent);
      if (payload) yield payload;
      boundary = findSSEBoundary(buffer);
    }

    if (done) break;
  }

  const trailingPayload = parseGeminiSSEPayload(buffer);
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

function parseGeminiSSEPayload(rawEvent: string): GeminiStreamPayload | undefined {
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
    return JSON.parse(data) as GeminiStreamPayload;
  } catch (err: any) {
    throw new Error(`Invalid Gemini stream payload: ${getDetailedErrorMessage(err)}`);
  }
}

function toGeminiContents(messages: Message[]): {
  systemInstruction?: GeminiContent;
  contents: GeminiContent[];
} {
  const systemParts: GeminiPart[] = [];
  const contents: GeminiContent[] = [];
  const toolNamesById = new Map<string, string>();

  for (const message of messages) {
    const content = normalizeMessageContent(message.content);
    if (message.role === 'system') {
      if (content) systemParts.push({ text: content });
      continue;
    }

    if (message.role === 'tool') {
      if (!message.toolCallId) {
        throw new Error('Zero Gemini provider requires toolCallId on tool result messages');
      }
      appendUserParts(contents, [
        {
          functionResponse: {
            id: message.toolCallId,
            name: toolNamesById.get(message.toolCallId) ?? message.toolCallId,
            response: { result: content },
          },
        },
      ]);
      continue;
    }

    if (message.role === 'assistant') {
      const parts: GeminiPart[] = [];
      if (content) parts.push({ text: content });
      for (const toolCall of message.toolCalls ?? []) {
        toolNamesById.set(toolCall.id, toolCall.name);
        parts.push({
          functionCall: {
            id: toolCall.id,
            name: toolCall.name,
            args: parseToolArguments(toolCall.arguments, toolCall.name),
          },
        });
      }
      if (parts.length > 0) contents.push({ role: 'model', parts });
      continue;
    }

    if (content) {
      appendUserParts(contents, [{ text: content }]);
    }
  }

  return {
    systemInstruction: systemParts.length > 0
      ? { role: 'system', parts: systemParts }
      : undefined,
    contents,
  };
}

function appendUserParts(contents: GeminiContent[], parts: GeminiPart[]): void {
  const last = contents.at(-1);
  if (last?.role === 'user') {
    last.parts = [...last.parts, ...parts];
    return;
  }
  contents.push({ role: 'user', parts });
}

function toGeminiTools(tools: ToolDefinition[]): Array<Record<string, unknown>> {
  return [
    {
      functionDeclarations: tools.map((tool) => ({
        name: tool.name,
        description: tool.description,
        parameters: tool.parameters,
      })),
    },
  ];
}

function parseToolArguments(argumentsJson: string, toolName: string): Record<string, unknown> {
  if (!argumentsJson) return {};
  try {
    const parsed = JSON.parse(argumentsJson);
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      return parsed as Record<string, unknown>;
    }
    throw new Error(
      `Zero Gemini provider requires tool arguments for ${toolName} to be a JSON object`
    );
  } catch (err: any) {
    if (err?.message?.includes('JSON object')) throw err;
    throw new Error(
      `Zero Gemini provider could not parse tool arguments for ${toolName} as JSON`
    );
  }
}

function normalizeFunctionCallArgs(value: unknown, toolName: string): Record<string, unknown> {
  if (value === undefined || value === null) return {};
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    return value as Record<string, unknown>;
  }
  throw new Error(
    `Zero Gemini provider requires streamed tool arguments for ${toolName} to be a JSON object`
  );
}

function normalizeMaxTokens(maxTokens: number): number {
  if (!Number.isFinite(maxTokens) || !Number.isInteger(maxTokens) || maxTokens < 1) {
    throw new Error('Zero Gemini provider maxTokens must be a positive integer');
  }
  return maxTokens;
}

function normalizeModel(model: string): string {
  const normalized = model.trim().replace(/^models\//, '');
  if (!normalized) throw new Error('Zero Gemini provider requires a model');
  return normalized;
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
