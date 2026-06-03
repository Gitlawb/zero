import { describe, expect, it } from 'bun:test';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'fs';
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
        USERPROFILE: home,
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

  it('prints config validation diagnostics with source and field paths', async () => {
    const home = mkdtempSync(join(tmpdir(), 'zero-config-cli-'));
    try {
      const configPath = join(home, '.config', 'zero', 'config.json');
      mkdirSync(join(home, '.config', 'zero'), { recursive: true });
      writeFileSync(
        configPath,
        JSON.stringify({
          providers: [{ name: 'broken', baseURL: 'not a url', model: 'm' }],
        }),
        'utf-8'
      );

      const result = await runZeroConfig(['--json'], {
        HOME: home,
        USERPROFILE: home,
      });

      expect(result.exitCode).toBe(1);
      expect(result.stderr.trim()).toBe('');

      const payload = JSON.parse(result.stdout);
      expect(payload.ok).toBe(false);
      expect(payload.layers.find((layer: { source: string }) => layer.source === 'user')).toMatchObject({
        status: 'invalid',
        path: configPath,
      });
      expect(payload.issues.find((issue: { id: string }) => issue.id === 'config.user.invalid')).toMatchObject({
        path: configPath,
        fieldPath: 'providers.0.baseURL',
      });
    } finally {
      rmSync(home, { recursive: true, force: true });
    }
  });
});
