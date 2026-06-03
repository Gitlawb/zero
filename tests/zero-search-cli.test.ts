import { describe, expect, it } from 'bun:test';
import { mkdtempSync, rmSync, writeFileSync } from 'fs';
import { tmpdir } from 'os';
import { join } from 'path';
import { ZERO_REDACTED_SECRET } from '../src/zero-redaction';
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

  it('redacts secrets from structured JSON search output', async () => {
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
        sessionId: 'json_secret_search',
        title: 'JSON Secret sk-proj-abcdefghijklmnopqrstuvwxyz1234567890',
        cwd: '/repo/zero?token=local-cwd-secret',
      });
      await store.appendEvent('json_secret_search', {
        type: 'tool_result',
        payload: {
          result:
            'diagnostic json includes OPENAI_API_KEY=sk-proj-abcdefghijklmnopqrstuvwxyz1234567890',
          authorization: 'Bearer local-authorization-secret',
          headers: {
            cookie: 'zero_session=local-cookie-secret',
          },
        },
      });

      const result = await runZeroSearch(
        ['--json', '--context', '240', 'diagnostic', 'json'],
        { XDG_DATA_HOME: dataHome }
      );

      expect(result.exitCode).toBe(0);
      expect(result.stderr.trim()).toBe('');
      expect(result.stdout).toContain(ZERO_REDACTED_SECRET);
      expect(result.stdout).not.toContain('sk-proj-abcdefghijklmnopqrstuvwxyz1234567890');
      expect(result.stdout).not.toContain('local-cwd-secret');
      expect(result.stdout).not.toContain('local-authorization-secret');
      expect(result.stdout).not.toContain('local-cookie-secret');
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

  it('forces a search index rebuild with --reindex', async () => {
    const dataHome = mkdtempSync(join(tmpdir(), 'zero-search-cli-'));
    try {
      const rootDir = join(dataHome, 'zero', 'sessions');
      const store = new ZeroSessionEventStore({
        rootDir,
        now: sequenceClock([
          '2026-06-03T00:00:00.000Z',
          '2026-06-03T00:00:01.000Z',
        ]),
      });
      await store.createSession({ sessionId: 'cli_reindex', title: 'CLI Reindex' });
      await store.appendEvent('cli_reindex', {
        type: 'message',
        payload: { content: 'force rebuild indexed content' },
      });

      const session = await store.getSession('cli_reindex');
      writeFileSync(
        join(rootDir, 'cli_reindex', 'search-index.json'),
        `${JSON.stringify({
          schemaVersion: 1,
          sessionId: 'cli_reindex',
          sessionUpdatedAt: session?.updatedAt,
          sessionEventCount: session?.eventCount,
          generatedAt: '2026-06-03T00:00:02.000Z',
          entries: [{
            sessionId: 'cli_reindex',
            eventId: 'cli_reindex:1',
            sequence: 1,
            type: 'message',
            createdAt: '2026-06-03T00:00:01.000Z',
            text: 'cached unrelated text',
          }],
        })}\n`,
        'utf-8'
      );

      const staleResult = await runZeroSearch(['--json', 'force', 'rebuild'], {
        XDG_DATA_HOME: dataHome,
      });
      expect(JSON.parse(staleResult.stdout).totalHits).toBe(0);

      const rebuiltResult = await runZeroSearch(['--json', '--reindex', 'force', 'rebuild'], {
        XDG_DATA_HOME: dataHome,
      });

      expect(rebuiltResult.exitCode).toBe(0);
      expect(rebuiltResult.stderr.trim()).toBe('');
      const payload = JSON.parse(rebuiltResult.stdout);
      expect(payload.totalHits).toBe(1);
      expect(payload.hits[0].event.id).toBe('cli_reindex:1');
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
