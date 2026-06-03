import { stat } from 'fs/promises';
import { resolve } from 'path';
import { runAgent } from '../agent/loop';
import { toolRegistry } from '../tools';
import {
  redactZeroErrorMessage,
  redactZeroSecrets,
  redactZeroString,
} from '../zero-redaction';
import {
  createZeroRunContext,
  ZeroRuntimeProviderError,
  ZeroRuntimeUsageError,
  type ZeroRunContext,
} from '../zero-runtime';
import {
  ZERO_STREAM_JSON_SCHEMA_VERSION,
  createZeroStreamJsonRunId,
  formatZeroStreamJsonEvent,
  parseZeroStreamJsonPrompt,
  type ZeroStreamJsonOutputEvent,
  type ZeroStreamJsonToolSideEffect,
} from '../zero-stream-json';

export type ExecInputFormat = 'text' | 'stream-json';
export type ExecOutputFormat = 'text' | 'json' | 'stream-json';

export const ZERO_EXEC_EXIT_CODES = {
  success: 0,
  crash: 1,
  usage: 2,
  provider: 3,
  tool: 4,
  permission: 5,
} as const;

export interface RunExecOptions {
  prompt?: string;
  file?: string;
  inputFormat?: string;
  model?: string;
  modelProfile?: string;
  reasoningEffort?: string;
  autonomy?: string;
  enabledTools?: string[];
  disabledTools?: string[];
  cwd?: string;
  outputFormat?: string;
  skipPermissionsUnsafe?: boolean;
  listTools?: boolean;
  maxTurns?: number;
  stdin?: string;
}

class ExecUsageError extends Error {}

export function parseExecOutputFormat(value: string | undefined): ExecOutputFormat | undefined {
  const normalized = (value || 'text').trim().toLowerCase();
  return normalized === 'text' || normalized === 'json' || normalized === 'stream-json'
    ? normalized
    : undefined;
}

export function parseExecInputFormat(value: string | undefined): ExecInputFormat | undefined {
  const normalized = (value || 'text').trim().toLowerCase();
  return normalized === 'text' || normalized === 'stream-json' ? normalized : undefined;
}

export async function resolveExecPrompt(
  options: Pick<RunExecOptions, 'prompt' | 'file' | 'inputFormat' | 'stdin'>
): Promise<string> {
  const inputFormat = parseExecInputFormat(options.inputFormat);
  if (!inputFormat) {
    throw new ExecUsageError(
      `Invalid input format "${options.inputFormat}". Expected "text" or "stream-json".`
    );
  }

  if (inputFormat === 'stream-json') {
    return resolveStreamJsonExecPrompt(options);
  }

  const parts: string[] = [];
  const inlinePrompt = options.prompt?.trim();

  if (inlinePrompt) {
    parts.push(inlinePrompt);
  }

  if (options.file) {
    const promptPath = resolve(options.file);
    const promptFile = Bun.file(promptPath);

    if (!(await promptFile.exists())) {
      throw new ExecUsageError(`Prompt file not found: ${promptPath}`);
    }

    const filePrompt = (await promptFile.text()).trim();
    if (!filePrompt) {
      throw new ExecUsageError(`Prompt file is empty: ${promptPath}`);
    }
    parts.push(filePrompt);
  }

  const prompt = parts.join('\n\n').trim();
  if (!prompt) {
    throw new ExecUsageError('Prompt required. Use `zero exec "prompt"` or `zero exec --file prompt.txt`.');
  }

  return prompt;
}

export async function runHeadless(prompt: string): Promise<void> {
  const exitCode = await runExec({ prompt, outputFormat: 'text' });
  if (exitCode !== ZERO_EXEC_EXIT_CODES.success) {
    process.exitCode = exitCode;
  }
}

export async function runExec(options: RunExecOptions): Promise<number> {
  const outputFormat = parseExecOutputFormat(options.outputFormat);
  if (!outputFormat) {
    writeUsageError(
      `Invalid output format "${options.outputFormat}". Expected "text", "json", or "stream-json".`
    );
    return ZERO_EXEC_EXIT_CODES.usage;
  }
  const inputFormat = parseExecInputFormat(options.inputFormat);
  if (!inputFormat) {
    writeUsageError(`Invalid input format "${options.inputFormat}". Expected "text" or "stream-json".`);
    return ZERO_EXEC_EXIT_CODES.usage;
  }

  const previousCwd = process.cwd();
  const runId = createZeroStreamJsonRunId();

  try {
    await changeWorkingDirectory(options.cwd);
    const prompt = options.listTools ? undefined : await resolveExecPrompt({
      ...options,
      inputFormat,
      stdin: options.stdin ?? await readStreamJsonStdinIfNeeded(inputFormat, options),
    });
    let context: ZeroRunContext;

    try {
      context = await createZeroRunContext({
        surface: 'exec',
        model: options.model,
        modelProfile: options.modelProfile,
        reasoningEffort: options.reasoningEffort,
        autonomy: options.autonomy,
        skipPermissionsUnsafe: options.skipPermissionsUnsafe,
        enabledTools: options.enabledTools,
        disabledTools: options.disabledTools,
        maxTurns: options.maxTurns,
      });
    } catch (err: any) {
      if (err instanceof ZeroRuntimeUsageError) {
        writeExecError(outputFormat, 'usage_error', err.message, {
          exitCode: ZERO_EXEC_EXIT_CODES.usage,
          recoverable: true,
          runId,
        });
        return ZERO_EXEC_EXIT_CODES.usage;
      }

      const message = err instanceof ZeroRuntimeProviderError ? err.message : formatProviderError(err);
      writeExecError(outputFormat, 'provider_error', message, {
        exitCode: ZERO_EXEC_EXIT_CODES.provider,
        recoverable: false,
        runId,
      });
      return ZERO_EXEC_EXIT_CODES.provider;
    }

    if (options.listTools) {
      writeToolList(outputFormat, context, runId);
      return ZERO_EXEC_EXIT_CODES.success;
    }

    if (context.permissionMode === 'unsafe') {
      writeWarning(outputFormat, 'Unsafe permissions are active for this run.', runId);
    }

    emitLegacyJson(outputFormat, {
      type: 'run_start',
      cwd: process.cwd(),
      provider: context.runtime.provider,
      model: context.modelId,
      model_profile: context.modelProfile?.id,
      model_label: context.modelLabel,
      api_model: context.runtime.apiModel,
      autonomy: context.autonomy,
      permission_mode: context.permissionMode,
      reasoning_effort: context.reasoningEffort,
      enabled_tools: context.enabledTools,
      disabled_tools: context.disabledTools,
      output_format: outputFormat,
    });
    emitStreamJson(outputFormat, {
      schemaVersion: ZERO_STREAM_JSON_SCHEMA_VERSION,
      type: 'run_start',
      runId,
      cwd: process.cwd(),
      provider: context.runtime.provider,
      model: context.modelId,
      apiModel: context.runtime.apiModel,
    });

    let streamedText = '';

    const finalAnswer = await runAgent(prompt!, context.provider, {
      ...context.agentOptions,
      onText: (text) => {
        streamedText += text;
        if (outputFormat === 'json') {
          emitLegacyJson(outputFormat, { type: 'text', delta: text });
        } else if (outputFormat === 'stream-json') {
          emitStreamJson(outputFormat, {
            schemaVersion: ZERO_STREAM_JSON_SCHEMA_VERSION,
            type: 'text',
            runId,
            delta: text,
          });
        } else {
          process.stdout.write(text);
        }
      },
      onToolCall: (toolCall) => {
        if (outputFormat === 'json') {
          emitLegacyJson(outputFormat, {
            type: 'tool_call',
            id: toolCall.id,
            name: toolCall.name,
            arguments: redactZeroString(toolCall.arguments),
          });
        } else if (outputFormat === 'stream-json') {
          emitStreamJson(outputFormat, {
            schemaVersion: ZERO_STREAM_JSON_SCHEMA_VERSION,
            type: 'tool_call',
            runId,
            id: toolCall.id,
            name: toolCall.name,
            args: parseToolArguments(toolCall.arguments),
            sideEffect: classifyToolSideEffect(toolCall.name),
          });
        } else {
          process.stderr.write(`[tool] ${toolCall.name}\n`);
        }
      },
      onToolResult: (result) => {
        if (outputFormat === 'json') {
          emitLegacyJson(outputFormat, {
            type: 'tool_result',
            tool_call_id: result.toolCallId,
            result: redactZeroString(result.result),
          });
        } else if (outputFormat === 'stream-json') {
          emitStreamJson(outputFormat, {
            schemaVersion: ZERO_STREAM_JSON_SCHEMA_VERSION,
            type: 'tool_result',
            runId,
            id: result.toolCallId,
            status: result.result.startsWith('Error') ? 'error' : 'ok',
            output: result.result,
            truncated: false,
          });
        } else {
          process.stderr.write(`[result] ${truncateForStatus(result.result)}\n`);
        }
      },
      onUsage: (usage) => {
        const totalTokens = (usage.promptTokens ?? 0) + (usage.completionTokens ?? 0);
        emitLegacyJson(outputFormat, {
          type: 'usage',
          prompt_tokens: usage.promptTokens,
          completion_tokens: usage.completionTokens,
          total_tokens: totalTokens,
        });
        emitStreamJson(outputFormat, {
          schemaVersion: ZERO_STREAM_JSON_SCHEMA_VERSION,
          type: 'usage',
          runId,
          promptTokens: usage.promptTokens,
          completionTokens: usage.completionTokens,
          totalTokens,
        });
      },
    });

    if (outputFormat === 'json') {
      emitLegacyJson(outputFormat, { type: 'final', text: finalAnswer });
      emitLegacyJson(outputFormat, { type: 'done', exit_code: ZERO_EXEC_EXIT_CODES.success });
    } else if (outputFormat === 'stream-json') {
      emitStreamJson(outputFormat, {
        schemaVersion: ZERO_STREAM_JSON_SCHEMA_VERSION,
        type: 'final',
        runId,
        text: finalAnswer,
      });
      emitStreamJson(outputFormat, {
        schemaVersion: ZERO_STREAM_JSON_SCHEMA_VERSION,
        type: 'run_end',
        runId,
        status: 'success',
        exitCode: ZERO_EXEC_EXIT_CODES.success,
      });
    } else {
      if (!streamedText && finalAnswer) {
        streamedText = finalAnswer;
        process.stdout.write(finalAnswer);
      }
      if (streamedText && !streamedText.endsWith('\n')) {
        process.stdout.write('\n');
      }
    }

    return ZERO_EXEC_EXIT_CODES.success;
  } catch (err: any) {
    if (err instanceof ExecUsageError) {
      writeExecError(outputFormat, 'usage_error', err.message, {
        exitCode: ZERO_EXEC_EXIT_CODES.usage,
        recoverable: true,
        runId,
      });
      return ZERO_EXEC_EXIT_CODES.usage;
    }

    writeExecError(outputFormat, 'crash', err?.message ?? String(err), {
      exitCode: ZERO_EXEC_EXIT_CODES.crash,
      recoverable: false,
      runId,
    });
    return ZERO_EXEC_EXIT_CODES.crash;
  } finally {
    if (process.cwd() !== previousCwd) {
      process.chdir(previousCwd);
    }
  }
}

async function resolveStreamJsonExecPrompt(
  options: Pick<RunExecOptions, 'prompt' | 'file' | 'stdin'>
): Promise<string> {
  if (options.prompt?.trim()) {
    throw new ExecUsageError(
      'Stream-json input does not accept positional prompt text. Pipe JSONL or use --file.'
    );
  }

  const inputs: string[] = [];
  if (options.file) {
    const promptPath = resolve(options.file);
    const promptFile = Bun.file(promptPath);

    if (!(await promptFile.exists())) {
      throw new ExecUsageError(`Stream-json input file not found: ${promptPath}`);
    }

    inputs.push(await promptFile.text());
  }

  if (options.stdin?.trim()) {
    inputs.push(options.stdin);
  }

  const input = inputs.join('\n').trim();
  if (!input) {
    throw new ExecUsageError('Stream-json input required. Pipe JSONL or use --file.');
  }

  try {
    return parseZeroStreamJsonPrompt(input);
  } catch (err: unknown) {
    throw new ExecUsageError(err instanceof Error ? err.message : String(err));
  }
}

async function readStreamJsonStdinIfNeeded(
  inputFormat: ExecInputFormat,
  options: RunExecOptions
): Promise<string | undefined> {
  if (inputFormat !== 'stream-json' || options.file || options.stdin !== undefined) {
    return undefined;
  }
  if (process.stdin.isTTY === true) return undefined;

  return Bun.stdin.text();
}

async function changeWorkingDirectory(cwd: string | undefined): Promise<void> {
  if (!cwd) return;

  const target = resolve(cwd);
  let info;
  try {
    info = await stat(target);
  } catch {
    throw new ExecUsageError(`Working directory not found: ${target}`);
  }

  if (!info.isDirectory()) {
    throw new ExecUsageError(`Working directory is not a directory: ${target}`);
  }

  process.chdir(target);
}

function writeUsageError(message: string): void {
  process.stderr.write(`[zero] ${message}\n`);
}

function writeExecError(
  format: ExecOutputFormat,
  code: string,
  message: string,
  options: {
    exitCode?: number;
    recoverable?: boolean;
    runId?: string;
  } = {}
): void {
  const safeMessage = redactZeroString(message);
  if (format === 'json') {
    emitLegacyJson(format, { type: 'error', code, message: safeMessage });
    return;
  }
  if (format === 'stream-json' && options.runId) {
    emitStreamJson(format, {
      schemaVersion: ZERO_STREAM_JSON_SCHEMA_VERSION,
      type: 'error',
      runId: options.runId,
      code,
      message: safeMessage,
      recoverable: options.recoverable ?? false,
    });

    if (options.exitCode !== undefined) {
      emitStreamJson(format, {
        schemaVersion: ZERO_STREAM_JSON_SCHEMA_VERSION,
        type: 'run_end',
        runId: options.runId,
        status: 'error',
        exitCode: options.exitCode,
      });
    }
    return;
  }

  process.stderr.write(`[zero] ${safeMessage}\n`);
}

function writeWarning(format: ExecOutputFormat, message: string, runId: string): void {
  const safeMessage = redactZeroString(message);
  if (format === 'json') {
    emitLegacyJson(format, { type: 'warning', message: safeMessage });
    return;
  }
  if (format === 'stream-json') {
    emitStreamJson(format, {
      schemaVersion: ZERO_STREAM_JSON_SCHEMA_VERSION,
      type: 'warning',
      runId,
      message: safeMessage,
    });
    return;
  }

  process.stderr.write(`[zero] WARNING: ${safeMessage}\n`);
}

function writeToolList(format: ExecOutputFormat, context: ZeroRunContext, runId: string): void {
  const tools = listContextTools(context);
  if (format === 'json') {
    emitLegacyJson(format, {
      type: 'tool_list',
      permission_mode: context.permissionMode,
      tools: tools.map((tool) => ({
        name: tool.name,
        side_effect: tool.safety.sideEffect,
        permission: tool.safety.permission,
        reason: tool.safety.reason,
      })),
    });
    return;
  }

  const text = formatToolList(context, tools);
  if (format === 'stream-json') {
    emitStreamJson(format, {
      schemaVersion: ZERO_STREAM_JSON_SCHEMA_VERSION,
      type: 'run_start',
      runId,
      cwd: process.cwd(),
      provider: context.runtime.provider,
      model: context.modelId,
      apiModel: context.runtime.apiModel,
    });
    emitStreamJson(format, {
      schemaVersion: ZERO_STREAM_JSON_SCHEMA_VERSION,
      type: 'final',
      runId,
      text,
    });
    emitStreamJson(format, {
      schemaVersion: ZERO_STREAM_JSON_SCHEMA_VERSION,
      type: 'run_end',
      runId,
      status: 'success',
      exitCode: ZERO_EXEC_EXIT_CODES.success,
    });
    return;
  }

  process.stdout.write(text);
}

function formatToolList(
  context: ZeroRunContext,
  tools: ReturnType<typeof listContextTools>
): string {
  let output = `Tools visible to model (${context.permissionMode}):\n`;
  for (const tool of tools) {
    output += `  ${tool.name.padEnd(14)} ${tool.safety.sideEffect.padEnd(16)} ${tool.safety.permission} - ${tool.safety.reason}\n`;
  }
  return output;
}

function listContextTools(context: ZeroRunContext) {
  const enabled = context.enabledTools ? new Set(context.enabledTools) : undefined;
  const disabled = new Set(context.disabledTools ?? []);

  return toolRegistry.getAll().filter((tool) => {
    if (enabled && !enabled.has(tool.name)) return false;
    if (disabled.has(tool.name)) return false;

    return context.permissionMode === 'unsafe' || context.permissionMode === 'ask'
      ? tool.safety.permission !== 'deny'
      : tool.safety.permission === 'allow';
  });
}

function emitLegacyJson(format: ExecOutputFormat, payload: Record<string, unknown>): void {
  if (format !== 'json') return;
  process.stdout.write(`${JSON.stringify(redactZeroSecrets(payload))}\n`);
}

function emitStreamJson(format: ExecOutputFormat, event: ZeroStreamJsonOutputEvent): void {
  if (format !== 'stream-json') return;
  process.stdout.write(`${formatZeroStreamJsonEvent(event)}\n`);
}

function parseToolArguments(value: string): unknown {
  if (!value.trim()) return {};
  try {
    return JSON.parse(value);
  } catch {
    return value;
  }
}

function classifyToolSideEffect(name: string): ZeroStreamJsonToolSideEffect {
  if (['glob', 'grep', 'list_directory', 'read_file'].includes(name)) return 'read';
  if (['apply_patch', 'edit_file', 'write_file'].includes(name)) return 'write';
  if (name === 'bash') return 'shell';
  return 'unknown';
}

function formatProviderError(err: any): string {
  return redactZeroErrorMessage(err);
}

function truncateForStatus(value: string): string {
  const compact = redactZeroString(value).replace(/\s+/g, ' ').trim();
  return compact.length > 200 ? `${compact.slice(0, 200)}...` : compact;
}
