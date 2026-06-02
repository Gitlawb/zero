import { describe, expect, it } from 'bun:test';
import { mkdtemp, rm, writeFile } from 'fs/promises';
import { join } from 'path';
import {
  parseExecOutputFormat,
  resolveExecPrompt,
  ZERO_EXEC_EXIT_CODES,
} from '../src/cli';

async function runZero(args: string[], envOverrides: NodeJS.ProcessEnv = {}) {
  const child = Bun.spawn([process.execPath, 'src/index.ts', ...args], {
    env: { ...process.env, ...envOverrides },
    stderr: 'pipe',
    stdout: 'pipe',
  });

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
    expect(result.stdout).toContain('--cwd');
    expect(result.stdout).toContain('--output-format');
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
});

describe('headless exec prompt helpers', () => {
  it('parses supported output formats', () => {
    expect(parseExecOutputFormat(undefined)).toBe('text');
    expect(parseExecOutputFormat('text')).toBe('text');
    expect(parseExecOutputFormat('json')).toBe('json');
    expect(parseExecOutputFormat('JSON')).toBe('json');
    expect(parseExecOutputFormat('xml')).toBeUndefined();
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
});
