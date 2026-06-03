import { describe, expect, it } from 'bun:test';
import { mkdtempSync, rmSync } from 'fs';
import { tmpdir } from 'os';
import { join } from 'path';
import { ZERO_REDACTED_SECRET } from '../src/zero-redaction';

async function runZeroDoctor(
  args: string[],
  envOverrides: NodeJS.ProcessEnv = {}
): Promise<{ exitCode: number; stdout: string; stderr: string }> {
  const child = Bun.spawn([process.execPath, 'src/index.ts', 'doctor', ...args], {
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

describe('zero doctor CLI', () => {
  it('prints redacted structured health checks', async () => {
    const home = mkdtempSync(join(tmpdir(), 'zero-doctor-cli-'));
    try {
      const result = await runZeroDoctor(['--json'], {
        HOME: home,
        OPENAI_API_KEY: 'sk-proj-abcdefghijklmnopqrstuvwxyz1234567890',
        OPENAI_MODEL: 'gpt-4.1',
      });

      expect(result.exitCode).toBe(0);
      expect(result.stderr.trim()).toBe('');
      expect(result.stdout).toContain(ZERO_REDACTED_SECRET);
      expect(result.stdout).not.toContain('sk-proj-abcdefghijklmnopqrstuvwxyz1234567890');

      const payload = JSON.parse(result.stdout);
      expect(payload.ok).toBe(true);
      expect(payload.checks.map((check: { id: string }) => check.id)).toEqual([
        'runtime.bun',
        'config.files',
        'provider.config',
        'provider.model',
        'provider.adapter',
        'provider.connectivity',
      ]);
    } finally {
      rmSync(home, { recursive: true, force: true });
    }
  });
});
