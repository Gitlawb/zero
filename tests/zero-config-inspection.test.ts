import { describe, expect, it } from 'bun:test';
import { mkdtempSync, rmSync, writeFileSync } from 'fs';
import { tmpdir } from 'os';
import { join } from 'path';
import { ZERO_REDACTED_SECRET } from '../src/zero-redaction';
import {
  formatZeroConfigInspection,
  inspectZeroConfig,
} from '../src/zero-config-inspection';

function freshTmp(): string {
  return mkdtempSync(join(tmpdir(), 'zero-config-inspection-'));
}

describe('Zero config inspection backend', () => {
  it('reports layered effective config and redacted provider details', () => {
    const tmp = freshTmp();
    try {
      const userConfigPath = join(tmp, 'user.json');
      const projectConfigPath = join(tmp, 'project.json');
      writeFileSync(
        userConfigPath,
        JSON.stringify({
          activeProvider: 'work',
          maxTurns: 20,
          providers: [{
            name: 'work',
            provider: 'openai',
            baseURL: 'https://api.openai.com/v1',
            model: 'gpt-4.1',
            apiKey: 'sk-proj-abcdefghijklmnopqrstuvwxyz1234567890',
          }],
        }),
        'utf-8'
      );
      writeFileSync(projectConfigPath, JSON.stringify({ maxTurns: 6 }), 'utf-8');

      const report = inspectZeroConfig({
        now: () => new Date('2026-06-03T00:00:00.000Z'),
        userConfigPath,
        projectConfigPath,
        env: { ZERO_DEBUG: 'true' },
      });

      expect(report.generatedAt).toBe('2026-06-03T00:00:00.000Z');
      expect(report.ok).toBe(true);
      expect(report.effective.maxTurns).toBe(6);
      expect(report.effective.debug).toBe(true);
      expect(report.layers.map((layer) => layer.source)).toEqual([
        'defaults',
        'user',
        'project',
        'env',
      ]);
      expect(report.layers.find((layer) => layer.source === 'user')).toMatchObject({
        present: true,
        status: 'loaded',
      });
      expect(report.provider).toMatchObject({
        configured: true,
        source: 'profile',
        profileName: 'work',
        model: 'gpt-4.1',
        apiKey: ZERO_REDACTED_SECRET,
      });

      const output = formatZeroConfigInspection(report);
      expect(output).toContain('[loaded] user');
      expect(output).toContain('provider: profile work');
      expect(output).not.toContain('sk-proj-abcdefghijklmnopqrstuvwxyz1234567890');
    } finally {
      rmSync(tmp, { recursive: true, force: true });
    }
  });

  it('surfaces invalid config files without leaking secrets', () => {
    const tmp = freshTmp();
    try {
      const userConfigPath = join(tmp, 'bad-user.json');
      writeFileSync(
        userConfigPath,
        JSON.stringify({
          providers: [{
            name: 'broken',
            provider: 'openai',
            baseURL: 'not a url',
            model: 'gpt-4.1',
            apiKey: 'sk-proj-abcdefghijklmnopqrstuvwxyz1234567890',
          }],
        }),
        'utf-8'
      );

      const report = inspectZeroConfig({
        now: () => new Date('2026-06-03T00:00:00.000Z'),
        userConfigPath,
        projectConfigPath: join(tmp, 'missing-project.json'),
        env: {},
      });
      const output = formatZeroConfigInspection(report);

      expect(report.ok).toBe(false);
      expect(report.layers.find((layer) => layer.source === 'user')).toMatchObject({
        present: true,
        status: 'invalid',
      });
      expect(report.issues.some((issue) => issue.id === 'config.user.invalid')).toBe(true);
      expect(output).toContain('[invalid] user');
      expect(output).toContain('config.user.invalid');
      expect(output).not.toContain('sk-proj-abcdefghijklmnopqrstuvwxyz1234567890');
    } finally {
      rmSync(tmp, { recursive: true, force: true });
    }
  });
});
