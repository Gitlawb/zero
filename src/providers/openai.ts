import OpenAI from 'openai';
import type { Provider, Message, ToolDefinition, StreamEvent } from './types';

interface OpenAIProviderOptions {
  apiKey: string;
  baseURL?: string;
  model: string;
}

function isUnsupportedStreamOptionsError(error: unknown): boolean {
  const candidate = error as {
    status?: number;
    code?: string;
    message?: string;
    error?: {
      code?: string;
      message?: string;
    };
  };

  const statusAllowsRetry = candidate.status === 400 || candidate.status === 422;
  const errorText = [
    candidate.code,
    candidate.message,
    candidate.error?.code,
    candidate.error?.message,
  ].filter(Boolean).join(' ').toLowerCase();

  return statusAllowsRetry && (
    errorText.includes('stream_options') ||
    errorText.includes('stream options') ||
    errorText.includes('include_usage') ||
    errorText.includes('unknown parameter') ||
    errorText.includes('unsupported parameter') ||
    errorText.includes('unrecognized')
  );
}

export class OpenAIProvider implements Provider {
  private client: OpenAI;
  private model: string;

  constructor({ apiKey, baseURL, model }: OpenAIProviderOptions) {
    this.client = new OpenAI({
      apiKey,
      baseURL: baseURL || 'https://api.openai.com/v1',
    });
    this.model = model;
  }

  async *streamCompletion(
    messages: Message[],
    tools: ToolDefinition[]
  ): AsyncIterable<StreamEvent> {
    const openaiMessages = messages.map((m) => ({
      role: m.role,
      content: m.content,
      tool_calls: m.toolCalls?.map((tc) => ({
        id: tc.id,
        type: 'function' as const,
        function: {
          name: tc.name,
          arguments: tc.arguments,
        },
      })),
      tool_call_id: m.toolCallId,
    }));

    const openaiTools = tools.length > 0
      ? tools.map((t) => ({
          type: 'function' as const,
          function: {
            name: t.name,
            description: t.description,
            parameters: t.parameters,
          },
        }))
      : undefined;

    const createStreamRequest = (includeUsage: boolean) => ({
      model: this.model,
      messages: openaiMessages as any,
      tools: openaiTools,
      stream: true as const,
      ...(includeUsage ? {
        stream_options: {
          include_usage: true,
        },
      } : {}),
    });

    let stream;
    try {
      stream = await this.client.chat.completions.create(createStreamRequest(true));
    } catch (error) {
      if (!isUnsupportedStreamOptionsError(error)) {
        throw error;
      }
      stream = await this.client.chat.completions.create(createStreamRequest(false));
    }

    const toolCallAccumulators = new Map<number, { 
      id: string; 
      name: string; 
      arguments: string;
      started: boolean;
    }>();

    for await (const chunk of stream) {
      const delta = chunk.choices[0]?.delta;
      const finishReason = chunk.choices[0]?.finish_reason;

      if (delta?.content) {
        yield { type: 'text', content: delta.content };
      }

      if (delta?.tool_calls) {
        for (const tc of delta.tool_calls) {
          if (tc.index === undefined) continue;

          let acc = toolCallAccumulators.get(tc.index);
          if (!acc) {
            acc = { id: '', name: '', arguments: '', started: false };
            toolCallAccumulators.set(tc.index, acc);
          }

          // If we already had data at this index and now get a new id, 
          // the previous tool call is complete.
          if (tc.id && acc.id && acc.id !== tc.id) {
            if (acc.id) {
              yield { type: 'tool-call-end', id: acc.id };
            }
            acc = { id: '', name: '', arguments: '', started: false };
            toolCallAccumulators.set(tc.index, acc);
          }

          if (tc.id) acc.id = tc.id;
          if (tc.function?.name) acc.name = tc.function.name;
          if (tc.function?.arguments) {
            acc.arguments += tc.function.arguments;
            yield {
              type: 'tool-call-delta',
              id: acc.id || `pending-${tc.index}`,
              argumentsFragment: tc.function.arguments,
            };
          }

          // Emit start event the first time we have both id and name
          if (acc.id && acc.name && !acc.started) {
            yield { type: 'tool-call-start', id: acc.id, name: acc.name };
            acc.started = true;
          }
        }
      }

      if (chunk.usage) {
        yield {
          type: 'usage',
          promptTokens: chunk.usage.prompt_tokens,
          completionTokens: chunk.usage.completion_tokens,
        };
      }

      // If the model signaled it's done with tool calls, close any open ones
      if (finishReason === 'tool_calls') {
        for (const [_, acc] of toolCallAccumulators) {
          if (acc.id) {
            yield { type: 'tool-call-end', id: acc.id };
          }
        }
      }
    }

    // End of stream - close any remaining open tool calls
    for (const [_, acc] of toolCallAccumulators) {
      if (acc.id) {
        yield { type: 'tool-call-end', id: acc.id };
      }
    }

    yield { type: 'done' };
  }
}
