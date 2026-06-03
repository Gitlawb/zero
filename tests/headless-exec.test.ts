import { describe, expect, it } from 'bun:test';
import { mkdtemp, rm, writeFile } from 'fs/promises';
import { join } from 'path';
import {
  parseExecInputFormat,
  parseExecOutputFormat,
  resolveExecPrompt,
  ZERO_EXEC_EXIT_CODES,
} from '../src/cli';
import { ZeroSessionEventStore } from '../src/zero-sessions';
import { ZERO_STREAM_JSON_SCHEMA_VERSION } from '../src/zero-stream-json';

async function runZero(
  args: string[],
  envOverrides: NodeJS.ProcessEnv = {},
  stdin?: string
) {
  const child = Bun.spawn([process.execPath, 'src/index.ts', ...args], {
    env: { ...process.env, ...envOverrides },
    stderr: 'pipe',
    stdin: stdin === undefined ? undefined : 'pipe',
    stdout: 'pipe',
  });

  if (stdin !== undefined) {
    child.stdin.write(stdin);
    child.stdin.end();
  }

  const [exitCode, stdout, stderr] = await Promise.all([
    child.exited,
    new Response(child.stdout).text(),
    new Response(child.stderr).text(),
  ]);

  return { exitCode, stdout, stderr };
}

describe('zero exec CLI surface', () => {
  it('documents the M1 headless flags', async () => {
    const result = await runZero(['exec', '--help']);

    expect(result.exitCode).toBe(0);
    expect(result.stderr.trim()).toBe('');
    expect(result.stdout).toContain('Usage: zero exec');
    expect(result.stdout).toContain('--file');
    expect(result.stdout).toContain('--model');
    expect(result.stdout).toContain('--profile');
    expect(result.stdout).toContain('--reasoning-effort');
    expect(result.stdout).toContain('--auto');
    expect(result.stdout).toContain('--enabled-tools');
    expect(result.stdout).toContain('--disabled-tools');
    expect(result.stdout).toContain('--list-tools');
    expect(result.stdout).toContain('--cwd');
    expect(result.stdout).toContain('--input-format');
    expect(result.stdout).toContain('--output-format');
    expect(result.stdout).toContain('--resume');
    expect(result.stdout).toContain('--fork');
    expect(result.stdout).toContain('stream-json');
    expect(result.stdout).toContain('--skip-permissions-unsafe');
  });

  it('returns usage exit code when no prompt is provided', async () => {
    const result = await runZero(['exec']);

    expect(result.exitCode).toBe(ZERO_EXEC_EXIT_CODES.usage);
    expect(result.stdout.trim()).toBe('');
    expect(result.stderr).toContain('Prompt required');
  });

  it('returns usage exit code for an invalid output format', async () => {
    const result = await runZero(['exec', '--output-format', 'xml', 'hello']);

    expect(result.exitCode).toBe(ZERO_EXEC_EXIT_CODES.usage);
    expect(result.stdout.trim()).toBe('');
    expect(result.stderr).toContain('Invalid output format');
    expect(result.stderr).toContain('stream-json');
  });

  it('returns usage exit code for an invalid input format', async () => {
    const result = await runZero(['exec', '--input-format', 'yaml', 'hello']);

    expect(result.exitCode).toBe(ZERO_EXEC_EXIT_CODES.usage);
    expect(result.stdout.trim()).toBe('');
    expect(result.stderr).toContain('Invalid input format');
  });

  it('returns usage exit code for invalid autonomy and tool filters', async () => {
    const badAuto = await runZero(['exec', '--auto', 'chaos', 'hello']);
    expect(badAuto.exitCode).toBe(ZERO_EXEC_EXIT_CODES.usage);
    expect(badAuto.stderr).toContain('Invalid autonomy level');

    const badTool = await runZero(['exec', '--enabled-tools', 'missing_tool', 'hello']);
    expect(badTool.exitCode).toBe(ZERO_EXEC_EXIT_CODES.usage);
    expect(badTool.stderr).toContain('Unknown tool');
  });

  it('lists visible tools without requiring a prompt', async () => {
    const dir = await mkdtemp(join(process.cwd(), '.zero-list-tools-test-'));
    try {
      const providerScript = join(dir, 'provider-command.js');
      await writeFile(
        providerScript,
        'console.log(JSON.stringify({ provider: "openai-compatible", base_url: "http://localhost/v1", model: "local-model" }));\n',
        'utf-8'
      );

      const result = await runZero(
        ['exec', '--list-tools', '--enabled-tools', 'read_file,grep'],
        {
          ZERO_PROVIDER_COMMAND: `${JSON.stringify(process.execPath)} ${JSON.stringify(providerScript)}`,
        }
      );

      expect(result.exitCode).toBe(ZERO_EXEC_EXIT_CODES.success);
      expect(result.stderr.trim()).toBe('');
      expect(result.stdout).toContain('Tools visible to model');
      expect(result.stdout).toContain('read_file');
      expect(result.stdout).toContain('grep');
      expect(result.stdout).not.toContain('bash');
    } finally {
      await rm(dir, { recursive: true, force: true });
    }
  });

  it('returns usage exit code for invalid resume and fork options', async () => {
    const dataHome = await mkdtemp(join(process.cwd(), '.zero-session-cli-'));
    try {
      const conflictWithoutProvider = await runZero(
        ['exec', '--resume', 'abc', '--fork', 'def', 'hello'],
        { XDG_DATA_HOME: dataHome, OPENAI_API_KEY: '' }
      );
      expect(conflictWithoutProvider.exitCode).toBe(ZERO_EXEC_EXIT_CODES.usage);
      expect(conflictWithoutProvider.stderr).toContain('Use either --resume or --fork, not both');
      expect(conflictWithoutProvider.stderr).not.toContain('No LLM provider configured');

      const conflict = await runZero(
        ['exec', '--resume', 'abc', '--fork', 'def', 'hello'],
        { XDG_DATA_HOME: dataHome, OPENAI_API_KEY: 'test-key' }
      );
      expect(conflict.exitCode).toBe(ZERO_EXEC_EXIT_CODES.usage);
      expect(conflict.stderr).toContain('Use either --resume or --fork, not both');

      const missingLatest = await runZero(
        ['exec', 'hello', '--resume'],
        { XDG_DATA_HOME: dataHome, OPENAI_API_KEY: 'test-key' }
      );
      expect(missingLatest.exitCode).toBe(ZERO_EXEC_EXIT_CODES.usage);
      expect(missingLatest.stderr).toContain('No Zero sessions available to resume');

      const missingFork = await runZero(
        ['exec', '--fork', 'missing', 'hello'],
        { XDG_DATA_HOME: dataHome, OPENAI_API_KEY: 'test-key' }
      );
      expect(missingFork.exitCode).toBe(ZERO_EXEC_EXIT_CODES.usage);
      expect(missingFork.stderr).toContain('Zero session not found: missing');
    } finally {
      await rm(dataHome, { recursive: true, force: true });
    }
  });

  it('returns provider exit code for provider runtime failures', async () => {
    const dir = await mkdtemp(join(process.cwd(), '.zero-provider-test-'));
    try {
      const providerScript = join(dir, 'provider-command.js');
      await writeFile(
        providerScript,
        'console.log(JSON.stringify({ model: "zero-test-unknown-model" }));\n',
        'utf-8'
      );

      const result = await runZero(
        ['exec', '--output-format', 'json', 'hello'],
        {
          ZERO_PROVIDER_COMMAND: `${JSON.stringify(process.execPath)} ${JSON.stringify(providerScript)}`,
        }
      );

      const events = result.stdout.trim().split('\n').map((line) => JSON.parse(line));
      expect(result.exitCode).toBe(ZERO_EXEC_EXIT_CODES.provider);
      expect(result.stderr.trim()).toBe('');
      expect(events[0]).toMatchObject({
        type: 'error',
        code: 'provider_error',
      });
      expect(events[0].message).toContain('Unknown Zero model');
    } finally {
      await rm(dir, { recursive: true, force: true });
    }
  });

  it('emits schema-versioned stream-json provider errors and run end', async () => {
    const dir = await mkdtemp(join(process.cwd(), '.zero-provider-test-'));
    try {
      const providerScript = join(dir, 'provider-command.js');
      await writeFile(
        providerScript,
        'console.log(JSON.stringify({ model: "zero-test-unknown-model" }));\n',
        'utf-8'
      );

      const result = await runZero(
        ['exec', '--output-format', 'stream-json', 'hello'],
        {
          ZERO_PROVIDER_COMMAND: `${JSON.stringify(process.execPath)} ${JSON.stringify(providerScript)}`,
        }
      );

      const events = result.stdout.trim().split('\n').map((line) => JSON.parse(line));
      expect(result.exitCode).toBe(ZERO_EXEC_EXIT_CODES.provider);
      expect(result.stderr.trim()).toBe('');
      expect(events).toHaveLength(2);
      expect(events[0]).toMatchObject({
        schemaVersion: ZERO_STREAM_JSON_SCHEMA_VERSION,
        type: 'error',
        code: 'provider_error',
        recoverable: false,
      });
      expect(events[0].message).toContain('Unknown Zero model');
      expect(events[1]).toMatchObject({
        schemaVersion: ZERO_STREAM_JSON_SCHEMA_VERSION,
        type: 'run_end',
        status: 'error',
        exitCode: ZERO_EXEC_EXIT_CODES.provider,
      });
    } finally {
      await rm(dir, { recursive: true, force: true });
    }
  });

  it('reads stream-json prompt events from piped stdin before provider setup', async () => {
    const dir = await mkdtemp(join(process.cwd(), '.zero-provider-test-'));
    try {
      const providerScript = join(dir, 'provider-command.js');
      await writeFile(
        providerScript,
        'console.log(JSON.stringify({ model: "zero-test-unknown-model" }));\n',
        'utf-8'
      );

      const result = await runZero(
        ['exec', '--input-format', 'stream-json', '--output-format', 'stream-json'],
        {
          ZERO_PROVIDER_COMMAND: `${JSON.stringify(process.execPath)} ${JSON.stringify(providerScript)}`,
        },
        `${JSON.stringify({
          schemaVersion: ZERO_STREAM_JSON_SCHEMA_VERSION,
          type: 'prompt',
          content: 'Read this from piped stdin.',
        })}\n`
      );

      const events = result.stdout.trim().split('\n').map((line) => JSON.parse(line));
      expect(result.exitCode).toBe(ZERO_EXEC_EXIT_CODES.provider);
      expect(result.stderr.trim()).toBe('');
      expect(events[0]).toMatchObject({
        schemaVersion: ZERO_STREAM_JSON_SCHEMA_VERSION,
        type: 'error',
        code: 'provider_error',
      });
      expect(events[0].message).not.toContain('Stream-json input required');
    } finally {
      await rm(dir, { recursive: true, force: true });
    }
  });

  it('redacts secrets from structured provider errors', async () => {
    const dir = await mkdtemp(join(process.cwd(), '.zero-provider-test-'));
    const leakedModel = ['sk-proj', 'abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGH'].join('-');
    try {
      const providerScript = join(dir, `${leakedModel}.js`);
      await writeFile(
        providerScript,
        `console.error(${JSON.stringify(`provider leaked ${leakedModel}`)}); process.exit(1);\n`,
        'utf-8'
      );

      const result = await runZero(
        ['exec', '--output-format', 'json', 'hello'],
        {
          ZERO_PROVIDER_COMMAND: `${JSON.stringify(process.execPath)} ${JSON.stringify(providerScript)}`,
        }
      );

      const events = result.stdout.trim().split('\n').map((line) => JSON.parse(line));
      expect(result.exitCode).toBe(ZERO_EXEC_EXIT_CODES.provider);
      expect(events[0]).toMatchObject({
        type: 'error',
        code: 'provider_error',
      });
      expect(events[0].message).toContain('[REDACTED]');
      expect(events[0].message).not.toContain(leakedModel);
    } finally {
      await rm(dir, { recursive: true, force: true });
    }
  });

  it('emits session id and records successful exec events', async () => {
    const dataHome = await mkdtemp(join(process.cwd(), '.zero-session-cli-'));
    const providerDir = await mkdtemp(join(process.cwd(), '.zero-provider-test-'));
    const server = Bun.serve({
      port: 0,
      fetch() {
        return new Response([
          'data: {"id":"chatcmpl_zero","object":"chat.completion.chunk","created":0,"model":"local-coder","choices":[{"index":0,"delta":{"content":"session recorded"},"finish_reason":null}]}\n\n',
          'data: {"id":"chatcmpl_zero","object":"chat.completion.chunk","created":0,"model":"local-coder","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}\n\n',
          'data: [DONE]\n\n',
        ].join(''), {
          headers: { 'content-type': 'text/event-stream' },
        });
      },
    });

    try {
      const providerScript = join(providerDir, 'provider-command.js');
      await writeFile(
        providerScript,
        `console.log(JSON.stringify({ provider: "openai-compatible", model: "local-coder", base_url: "http://127.0.0.1:${server.port}/v1" }));\n`,
        'utf-8'
      );

      const result = await runZero(
        ['exec', '--model', 'local-coder', '--output-format', 'stream-json', 'persist this'],
        {
          XDG_DATA_HOME: dataHome,
          ZERO_PROVIDER_COMMAND: `${JSON.stringify(process.execPath)} ${JSON.stringify(providerScript)}`,
        }
      );

      expect(result.exitCode).toBe(0);
      expect(result.stderr.trim()).toBe('');
      const events = result.stdout.trim().split('\n').map((line) => JSON.parse(line));
      const runStart = events.find((event) => event.type === 'run_start');
      const sessionId = runStart?.sessionId;
      expect(runStart).toMatchObject({
        schemaVersion: ZERO_STREAM_JSON_SCHEMA_VERSION,
      });
      expect(typeof sessionId).toBe('string');
      expect(events.find((event) => event.type === 'final')).toMatchObject({
        text: 'session recorded',
      });

      const store = new ZeroSessionEventStore({
        rootDir: join(dataHome, 'zero', 'sessions'),
      });
      const sessions = await store.listSessions();
      expect(sessions).toHaveLength(1);
      expect(sessions[0]).toMatchObject({
        sessionId,
        eventCount: 2,
        lastEventType: 'message',
      });
      const recorded = await store.readEvents(sessionId);
      expect(recorded.map((event) => [event.type, (event.payload as any).role])).toEqual([
        ['message', 'user'],
        ['message', 'assistant'],
      ]);
      expect((recorded[1]?.payload as any).content).toBe('session recorded');
    } finally {
      server.stop(true);
      await rm(dataHome, { recursive: true, force: true });
      await rm(providerDir, { recursive: true, force: true });
    }
  });
});

describe('headless exec prompt helpers', () => {
  it('parses supported output formats', () => {
    expect(parseExecOutputFormat(undefined)).toBe('text');
    expect(parseExecOutputFormat('text')).toBe('text');
    expect(parseExecOutputFormat('json')).toBe('json');
    expect(parseExecOutputFormat('stream-json')).toBe('stream-json');
    expect(parseExecOutputFormat('JSON')).toBe('json');
    expect(parseExecOutputFormat('xml')).toBeUndefined();
  });

  it('parses supported input formats', () => {
    expect(parseExecInputFormat(undefined)).toBe('text');
    expect(parseExecInputFormat('text')).toBe('text');
    expect(parseExecInputFormat('stream-json')).toBe('stream-json');
    expect(parseExecInputFormat('STREAM-JSON')).toBe('stream-json');
    expect(parseExecInputFormat('yaml')).toBeUndefined();
  });

  it('combines inline and file prompts', async () => {
    const dir = await mkdtemp(join(process.cwd(), '.zero-exec-test-'));
    try {
      const promptPath = join(dir, 'prompt.txt');
      await writeFile(promptPath, 'from file\n', 'utf-8');

      const prompt = await resolveExecPrompt({
        prompt: 'from cli',
        file: promptPath,
      });

      expect(prompt).toBe('from cli\n\nfrom file');
    } finally {
      await rm(dir, { recursive: true, force: true });
    }
  });

  it('resolves prompts from stream-json stdin input', async () => {
    const prompt = await resolveExecPrompt({
      inputFormat: 'stream-json',
      stdin: [
        JSON.stringify({
          schemaVersion: ZERO_STREAM_JSON_SCHEMA_VERSION,
          type: 'message',
          role: 'user',
          content: 'Review the changed files.',
        }),
        JSON.stringify({
          schemaVersion: ZERO_STREAM_JSON_SCHEMA_VERSION,
          type: 'prompt',
          content: 'Return only blockers.',
        }),
      ].join('\n'),
    });

    expect(prompt).toBe('Review the changed files.\n\nReturn only blockers.');
  });
});
