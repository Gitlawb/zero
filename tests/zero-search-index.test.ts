import { describe, expect, it } from 'bun:test';
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'fs';
import { tmpdir } from 'os';
import { join } from 'path';
import { ZERO_REDACTED_SECRET } from '../src/zero-redaction';
import { ZeroSessionEventStore } from '../src/zero-sessions';
import { searchZeroSessions } from '../src/zero-search';

function tempRoot(): string {
  return mkdtempSync(join(tmpdir(), 'zero-search-index-'));
}

describe('Zero session search index', () => {
  it('builds a persistent redacted search index and reuses it for unchanged sessions', async () => {
    const rootDir = tempRoot();
    try {
      const store = new ZeroSessionEventStore({
        rootDir,
        now: sequenceClock([
          '2026-06-03T00:00:00.000Z',
          '2026-06-03T00:00:01.000Z',
        ]),
      });

      await store.createSession({ sessionId: 'indexed_session', title: 'Indexed' });
      await store.appendEvent('indexed_session', {
        type: 'tool_result',
        payload: {
          result:
            'alpha indexed result with OPENAI_API_KEY=sk-proj-abcdefghijklmnopqrstuvwxyz1234567890',
        },
      });

      const first = await searchZeroSessions('alpha indexed', { store });
      expect(first.totalHits).toBe(1);

      const indexPath = join(rootDir, 'indexed_session', 'search-index.json');
      expect(existsSync(indexPath)).toBe(true);
      const indexPayload = JSON.parse(readFileSync(indexPath, 'utf-8'));
      expect(indexPayload).toMatchObject({
        schemaVersion: 1,
        sessionId: 'indexed_session',
        sessionEventCount: 1,
      });
      expect(indexPayload.entries[0]).toMatchObject({
        sessionId: 'indexed_session',
        eventId: 'indexed_session:1',
        sequence: 1,
        type: 'tool_result',
      });
      expect(JSON.stringify(indexPayload)).toContain(ZERO_REDACTED_SECRET);
      expect(JSON.stringify(indexPayload)).not.toContain('sk-proj-abcdefghijklmnopqrstuvwxyz1234567890');

      writeFileSync(join(rootDir, 'indexed_session', 'events.jsonl'), '', 'utf-8');

      const cached = await searchZeroSessions('alpha indexed', { store });
      expect(cached.totalHits).toBe(1);
      expect(cached.hits[0]?.event.id).toBe('indexed_session:1');
    } finally {
      rmSync(rootDir, { recursive: true, force: true });
    }
  });

  it('rebuilds stale indexes and supports forced reindexing', async () => {
    const rootDir = tempRoot();
    try {
      const store = new ZeroSessionEventStore({
        rootDir,
        now: sequenceClock([
          '2026-06-03T00:00:00.000Z',
          '2026-06-03T00:00:01.000Z',
          '2026-06-03T00:00:02.000Z',
        ]),
      });

      await store.createSession({ sessionId: 'stale_index' });
      await store.appendEvent('stale_index', {
        type: 'message',
        payload: { content: 'old indexed term' },
      });
      await searchZeroSessions('old indexed', { store });

      await store.appendEvent('stale_index', {
        type: 'message',
        payload: { content: 'fresh indexed term' },
      });

      const rebuilt = await searchZeroSessions('fresh indexed', { store });
      expect(rebuilt.totalHits).toBe(1);
      expect(rebuilt.hits[0]?.event.id).toBe('stale_index:2');

      const session = await store.getSession('stale_index');
      const indexPath = join(rootDir, 'stale_index', 'search-index.json');
      writeFileSync(
        indexPath,
        `${JSON.stringify({
          schemaVersion: 1,
          sessionId: 'stale_index',
          sessionUpdatedAt: session?.updatedAt,
          sessionEventCount: session?.eventCount,
          generatedAt: '2026-06-03T00:00:03.000Z',
          entries: [
            {
              sessionId: 'stale_index',
              eventId: 'stale_index:1',
              sequence: 1,
              type: 'message',
              createdAt: '2026-06-03T00:00:01.000Z',
              text: 'cached unrelated text',
            },
            {
              sessionId: 'stale_index',
              eventId: 'stale_index:2',
              sequence: 2,
              type: 'message',
              createdAt: '2026-06-03T00:00:02.000Z',
              text: 'cached unrelated text',
            },
          ],
        })}\n`,
        'utf-8'
      );

      const cachedEmpty = await searchZeroSessions('fresh indexed', { store });
      expect(cachedEmpty.totalHits).toBe(0);

      const forced = await searchZeroSessions('fresh indexed', { store, reindex: true });
      expect(forced.totalHits).toBe(1);
      expect(forced.hits[0]?.event.id).toBe('stale_index:2');
    } finally {
      rmSync(rootDir, { recursive: true, force: true });
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
