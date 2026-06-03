import { stat } from 'fs/promises';
import { resolve } from 'path';
import { runAgent } from '../agent/loop';
import { loadProviderConfig } from '../config/provider';
import type { Provider } from '../providers/types';
import {
  createZeroProvider,
  resolveZeroProviderRuntime,
  ZeroPendingProviderError,
} from '../zero-provider-runtime';
import type { ZeroResolvedProviderRuntime } from '../zero-provider-runtime';

export type ExecOutputFormat = 'text' | 'json';

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
  model?: string;
  cwd?: string;
  outputFormat?: string;
  skipPermissionsUnsafe?: boolean;
  maxTurns?: number;
}

class ExecUsageError extends Error {}

export function parseExecOutputFormat(value: string | undefined): ExecOutputFormat | undefined {
  const normalized = (value || 'text').trim().toLowerCase();
  return normalized === 'text' || normalized === 'json' ? normalized : undefined;
}

export async function resolveExecPrompt(options: Pick<RunExecOptions, 'prompt' | 'file'>): Promise<string> {
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
    writeUsageError(`Invalid output format "${options.outputFormat}". Expected "text" or "json".`);
    return ZERO_EXEC_EXIT_CODES.usage;
  }

  const previousCwd = process.cwd();

  try {
    await changeWorkingDirectory(options.cwd);
    const prompt = await resolveExecPrompt(options);

    let runtime: ZeroResolvedProviderRuntime | undefined;
    let provider: Provider;

    try {
      const providerConfig = await loadProviderConfig();
      runtime = resolveZeroProviderRuntime({
        provider: providerConfig.provider,
        apiKey: providerConfig.apiKey,
        baseURL: providerConfig.baseURL,
        model: options.model?.trim() || providerConfig.model,
        profileName: providerConfig.profileName,
        source: providerConfig.source,
      });
      provider = createZeroProvider(runtime);
    } catch (err: any) {
      writeExecError(outputFormat, 'provider_error', formatProviderError(err));
      return ZERO_EXEC_EXIT_CODES.provider;
    }

    if (options.skipPermissionsUnsafe) {
      writeWarning(outputFormat, '--skip-permissions-unsafe grants prompt-gated tools for this run.');
    }

    emitJson(outputFormat, {
      type: 'run_start',
      cwd: process.cwd(),
      provider: runtime.provider,
      model: runtime.modelId ?? runtime.requestedModel,
      api_model: runtime.apiModel,
      output_format: outputFormat,
    });

    let streamedText = '';

    const finalAnswer = await runAgent(prompt, provider, {
      maxTurns: options.maxTurns,
      permissionMode: options.skipPermissionsUnsafe ? 'unsafe' : 'auto',
      onText: (text) => {
        streamedText += text;
        if (outputFormat === 'json') {
          emitJson(outputFormat, { type: 'text', delta: text });
        } else {
          process.stdout.write(text);
        }
      },
      onToolCall: (toolCall) => {
        if (outputFormat === 'json') {
          emitJson(outputFormat, {
            type: 'tool_call',
            id: toolCall.id,
            name: toolCall.name,
            arguments: toolCall.arguments,
          });
        } else {
          process.stderr.write(`[tool] ${toolCall.name}\n`);
        }
      },
      onToolResult: (result) => {
        if (outputFormat === 'json') {
          emitJson(outputFormat, {
            type: 'tool_result',
            tool_call_id: result.toolCallId,
            result: result.result,
          });
        } else {
          process.stderr.write(`[result] ${truncateForStatus(result.result)}\n`);
        }
      },
    });

    if (outputFormat === 'json') {
      emitJson(outputFormat, { type: 'final', text: finalAnswer });
      emitJson(outputFormat, { type: 'done', exit_code: ZERO_EXEC_EXIT_CODES.success });
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
      writeExecError(outputFormat, 'usage_error', err.message);
      return ZERO_EXEC_EXIT_CODES.usage;
    }

    writeExecError(outputFormat, 'crash', err?.message ?? String(err));
    return ZERO_EXEC_EXIT_CODES.crash;
  } finally {
    if (process.cwd() !== previousCwd) {
      process.chdir(previousCwd);
    }
  }
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

function writeExecError(format: ExecOutputFormat, code: string, message: string): void {
  if (format === 'json') {
    emitJson(format, { type: 'error', code, message });
    return;
  }

  process.stderr.write(`[zero] ${message}\n`);
}

function writeWarning(format: ExecOutputFormat, message: string): void {
  if (format === 'json') {
    emitJson(format, { type: 'warning', message });
    return;
  }

  process.stderr.write(`[zero] WARNING: ${message}\n`);
}

function emitJson(format: ExecOutputFormat, payload: Record<string, unknown>): void {
  if (format !== 'json') return;
  process.stdout.write(`${JSON.stringify(payload)}\n`);
}

function formatProviderError(err: any): string {
  const message = err?.message ?? String(err);
  if (err instanceof ZeroPendingProviderError) {
    return `${message}\nUse an implemented provider, or set provider: "openai-compatible" with a custom gateway.`;
  }

  return message;
}

function truncateForStatus(value: string): string {
  const compact = value.replace(/\s+/g, ' ').trim();
  return compact.length > 200 ? `${compact.slice(0, 200)}...` : compact;
}
