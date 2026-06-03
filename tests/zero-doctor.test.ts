import { describe, expect, it } from 'bun:test';
import { mkdtempSync, rmSync, writeFileSync } from 'fs';
import { tmpdir } from 'os';
import { join } from 'path';
import { ZERO_REDACTED_SECRET } from '../src/zero-redaction';
import {
  formatZeroDoctorReport,
  runZeroDoctor,
} from '../src/zero-doctor';

function freshTmp(): string {
  return mkdtempSync(join(tmpdir(), 'zero-doctor-'));
}

describe('Zero doctor backend', () => {
  it('reports pass and warn checks for a valid offline provider setup', async () => {
    const tmp = freshTmp();
    try {
      const report = await runZeroDoctor({
        now: () => new Date('2026-06-03T00:00:00.000Z'),
        bunVersion: '1.3.14',
        userConfigPath: join(tmp, 'no-user.json'),
        projectConfigPath: join(tmp, 'no-project.json'),
        env: {},
        providerConfig: {
          provider: 'openai',
          apiKey: 'sk-proj-abcdefghijklmnopqrstuvwxyz1234567890',
          baseURL: 'https://api.openai.com/v1',
          model: 'gpt-4.1',
          source: 'environment',
        },
      });

      expect(report.generatedAt).toBe('2026-06-03T00:00:00.000Z');
      expect(report.ok).toBe(true);
      expect(report.checks.map((check) => check.id)).toEqual([
        'runtime.bun',
        'config.files',
        'provider.config',
        'provider.model',
        'provider.adapter',
        'provider.connectivity',
      ]);
      expect(report.checks.find((check) => check.id === 'provider.model')).toMatchObject({
        status: 'pass',
        message: 'Model gpt-4.1 resolves to openai.',
      });
      expect(report.checks.find((check) => check.id === 'provider.connectivity')).toMatchObject({
        status: 'warn',
      });

      const output = formatZeroDoctorReport(report);
      expect(output).toContain('[pass] runtime.bun');
      expect(output).toContain('[warn] provider.connectivity');
      expect(output).not.toContain('sk-proj-abcdefghijklmnopqrstuvwxyz1234567890');
    } finally {
      rmSync(tmp, { recursive: true, force: true });
    }
  });

  it('fails invalid config files and redacts provider diagnostics', async () => {
    const tmp = freshTmp();
    try {
      const userConfigPath = join(tmp, 'config.json');
      writeFileSync(
        userConfigPath,
        JSON.stringify({
          providers: [
            {
              name: 'broken',
              provider: 'openai',
              baseURL: 'not a url',
              model: 'gpt-4.1',
              apiKey: 'sk-proj-abcdefghijklmnopqrstuvwxyz1234567890',
            },
          ],
        }),
        'utf-8'
      );

      const report = await runZeroDoctor({
        now: () => new Date('2026-06-03T00:00:00.000Z'),
        bunVersion: '1.3.14',
        userConfigPath,
        projectConfigPath: join(tmp, 'no-project.json'),
        env: {},
        providerConfig: {
          provider: 'openai',
          apiKey: 'sk-proj-abcdefghijklmnopqrstuvwxyz1234567890',
          baseURL: 'https://api.openai.com/v1?token=local-provider-secret',
          model: 'not-in-registry',
          source: 'environment',
        },
      });
      const output = formatZeroDoctorReport(report);

      expect(report.ok).toBe(false);
      expect(report.checks.find((check) => check.id === 'config.files')?.status).toBe('fail');
      expect(report.checks.find((check) => check.id === 'provider.model')?.status).toBe('fail');
      expect(output).toContain(ZERO_REDACTED_SECRET);
      expect(output).not.toContain('sk-proj-abcdefghijklmnopqrstuvwxyz1234567890');
      expect(output).not.toContain('local-provider-secret');
    } finally {
      rmSync(tmp, { recursive: true, force: true });
    }
  });
});
