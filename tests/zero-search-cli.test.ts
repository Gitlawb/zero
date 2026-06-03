import { describe, expect, it } from 'bun:test';
import { mkdtempSync, rmSync } from 'fs';
import { tmpdir } from 'os';
import { join } from 'path';
import { ZeroSessionEventStore } from '../src/zero-sessions';

async function runZeroSearch(
  args: string[],
  envOverrides: NodeJS.ProcessEnv = {}
): Promise<{ exitCode: number; stdout: string; stderr: string }> {
  const child = Bun.spawn([process.execPath, 'src/index.ts', 'search', ...args], {
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

describe('zero search CLI', () => {
  it('prints structured JSON search results from the default session root', async () => {
    const dataHome = mkdtempSync(join(tmpdir(), 'zero-search-cli-'));
    try {
      const store = new ZeroSessionEventStore({
        rootDir: join(dataHome, 'zero', 'sessions'),
        now: sequenceClock([
          '2026-06-03T00:00:00.000Z',
          '2026-06-03T00:00:01.000Z',
        ]),
      });
      await store.createSession({
        sessionId: 'cli_search',
        title: 'CLI Search',
        cwd: '/repo/zero',
      });
      await store.appendEvent('cli_search', {
        type: 'tool_result',
        payload: { result: 'local zero search json output' },
      });

      const result = await runZeroSearch(
        ['--json', '--limit', '5', '--context', '12', 'zero', 'search'],
        { XDG_DATA_HOME: dataHome }
      );

      expect(result.exitCode).toBe(0);
      expect(result.stderr.trim()).toBe('');
      const payload = JSON.parse(result.stdout);
      expect(payload).toMatchObject({
        query: 'zero search',
        normalizedQuery: 'zero search',
        searchedSessions: 1,
        totalHits: 1,
      });
      expect(payload.hits[0]).toMatchObject({
        session: {
          sessionId: 'cli_search',
          title: 'CLI Search',
        },
        event: {
          id: 'cli_search:1',
          type: 'tool_result',
        },
      });
      expect(payload.hits[0].context).toContain('zero search');
    } finally {
      rmSync(dataHome, { recursive: true, force: true });
    }
  });

  it('prints readable text output and returns a usage error for invalid options', async () => {
    const dataHome = mkdtempSync(join(tmpdir(), 'zero-search-cli-'));
    try {
      const store = new ZeroSessionEventStore({
        rootDir: join(dataHome, 'zero', 'sessions'),
        now: sequenceClock([
          '2026-06-03T00:00:00.000Z',
          '2026-06-03T00:00:01.000Z',
        ]),
      });
      await store.createSession({ sessionId: 'text_search', title: 'Text Search' });
      await store.appendEvent('text_search', {
        type: 'message',
        payload: { content: 'searchable text output from session events' },
      });

      const textResult = await runZeroSearch(['searchable', 'output'], {
        XDG_DATA_HOME: dataHome,
      });
      expect(textResult.exitCode).toBe(0);
      expect(textResult.stderr.trim()).toBe('');
      expect(textResult.stdout).toContain('Found 1 local session event');
      expect(textResult.stdout).toContain('text_search #1 message - Text Search');

      const invalidResult = await runZeroSearch(['--limit', 'zero', 'anything'], {
        XDG_DATA_HOME: dataHome,
      });
      expect(invalidResult.exitCode).toBe(2);
      expect(invalidResult.stdout.trim()).toBe('');
      expect(invalidResult.stderr).toContain('Invalid --limit value');
    } finally {
      rmSync(dataHome, { recursive: true, force: true });
    }
  });
});

function sequenceClock(values: string[]): () => Date {
  let index = 0;
  return () => {
    const value = values[Math.min(index, values.length - 1)];
    index += 1;
    return new Date(value ?? '2026-06-03T00:00:00.000Z');
  };
}
