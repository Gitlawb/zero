import { describe, expect, it } from 'bun:test';
import { mkdtempSync, rmSync } from 'fs';
import { tmpdir } from 'os';
import { join } from 'path';
import { ZERO_REDACTED_SECRET } from '../src/zero-redaction';

async function runZeroConfig(
  args: string[],
  envOverrides: NodeJS.ProcessEnv = {}
): Promise<{ exitCode: number; stdout: string; stderr: string }> {
  const child = Bun.spawn([process.execPath, 'src/index.ts', 'config', ...args], {
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

describe('zero config CLI', () => {
  it('prints redacted structured config inspection', async () => {
    const home = mkdtempSync(join(tmpdir(), 'zero-config-cli-'));
    try {
      const result = await runZeroConfig(['--json'], {
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
      expect(payload.provider).toMatchObject({
        configured: true,
        source: 'environment',
        model: 'gpt-4.1',
        apiKey: ZERO_REDACTED_SECRET,
      });
      expect(payload.layers.map((layer: { source: string }) => layer.source)).toEqual([
        'defaults',
        'env',
      ]);
    } finally {
      rmSync(home, { recursive: true, force: true });
    }
  });
});
