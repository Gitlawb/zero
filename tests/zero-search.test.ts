import { describe, expect, it } from 'bun:test';
import { mkdtempSync, rmSync } from 'fs';
import { tmpdir } from 'os';
import { join } from 'path';
import { ZeroSessionEventStore } from '../src/zero-sessions';
import { ZERO_REDACTED_SECRET } from '../src/zero-redaction';
import {
  formatZeroSearchResult,
  searchZeroSessions,
} from '../src/zero-search';

function tempRoot(): string {
  return mkdtempSync(join(tmpdir(), 'zero-search-'));
}

describe('Zero session search backend', () => {
  it('returns metadata-rich local event hits in latest-session order', async () => {
    const rootDir = tempRoot();
    try {
      const store = new ZeroSessionEventStore({
        rootDir,
        now: sequenceClock([
          '2026-06-03T00:00:00.000Z',
          '2026-06-03T00:00:01.000Z',
          '2026-06-03T00:00:02.000Z',
          '2026-06-03T00:00:03.000Z',
        ]),
      });

      await store.createSession({
        sessionId: 'older_search',
        title: 'Older search work',
        cwd: '/repo/zero',
        modelId: 'gpt-4.1',
        provider: 'openai',
      });
      await store.appendEvent('older_search', {
        type: 'tool_result',
        payload: {
          toolCallId: 'call_1',
          result: 'The Zero search backend reads append-only event JSONL.',
        },
      });
      await store.createSession({
        sessionId: 'newer_search',
        title: 'Newer search work',
        cwd: '/repo/zero/packages/cli',
        modelId: 'claude-sonnet-4',
        provider: 'anthropic',
      });
      await store.appendEvent('newer_search', {
        type: 'message',
        payload: {
          role: 'user',
          content: 'Please add zero search command output.',
        },
      });

      const result = await searchZeroSessions('ZERO search', {
        store,
        contextChars: 18,
        limit: 10,
      });

      expect(result.query).toBe('ZERO search');
      expect(result.normalizedQuery).toBe('zero search');
      expect(result.rootDir).toBe(rootDir);
      expect(result.searchedSessions).toBe(2);
      expect(result.totalHits).toBe(2);
      expect(result.hits.map((hit) => hit.session.sessionId)).toEqual([
        'newer_search',
        'older_search',
      ]);
      expect(result.hits[0]).toMatchObject({
        event: {
          id: 'newer_search:1',
          sequence: 1,
          type: 'message',
        },
        session: {
          title: 'Newer search work',
          provider: 'anthropic',
          modelId: 'claude-sonnet-4',
        },
      });
      expect(result.hits[0]?.context).toContain('zero search command');
    } finally {
      rmSync(rootDir, { recursive: true, force: true });
    }
  });

  it('filters by session id and event type before applying the result limit', async () => {
    const rootDir = tempRoot();
    try {
      const store = new ZeroSessionEventStore({
        rootDir,
        now: sequenceClock([
          '2026-06-03T00:00:00.000Z',
          '2026-06-03T00:00:01.000Z',
          '2026-06-03T00:00:02.000Z',
          '2026-06-03T00:00:03.000Z',
        ]),
      });

      await store.createSession({ sessionId: 'target_session', title: 'Target' });
      await store.appendEvent('target_session', {
        type: 'message',
        payload: { content: 'search term from user message' },
      });
      await store.appendEvent('target_session', {
        type: 'tool_result',
        payload: { result: 'search term from tool result' },
      });
      await store.createSession({ sessionId: 'other_session', title: 'Other' });
      await store.appendEvent('other_session', {
        type: 'tool_result',
        payload: { result: 'search term in another session' },
      });

      const result = await searchZeroSessions('search term', {
        store,
        sessionId: 'target_session',
        type: 'tool_result',
        limit: 1,
      });

      expect(result.searchedSessions).toBe(1);
      expect(result.totalHits).toBe(1);
      expect(result.hits[0]?.session.sessionId).toBe('target_session');
      expect(result.hits[0]?.event.type).toBe('tool_result');
      expect(result.hits[0]?.context).toContain('tool result');
    } finally {
      rmSync(rootDir, { recursive: true, force: true });
    }
  });

  it('formats text output for hits and empty results', async () => {
    const rootDir = tempRoot();
    try {
      const store = new ZeroSessionEventStore({
        rootDir,
        now: sequenceClock([
          '2026-06-03T00:00:00.000Z',
          '2026-06-03T00:00:01.000Z',
        ]),
      });

      await store.createSession({
        sessionId: 'format_session',
        title: 'Format Session',
        cwd: '/repo/zero',
      });
      await store.appendEvent('format_session', {
        type: 'message',
        payload: { content: 'formatting zero search result text' },
      });

      const hitResult = await searchZeroSessions('search result', { store });
      expect(formatZeroSearchResult(hitResult)).toContain(
        '1. format_session #1 message - Format Session'
      );
      expect(formatZeroSearchResult(hitResult)).toContain(
        'formatting zero search result text'
      );

      const emptyResult = await searchZeroSessions('missing', { store });
      expect(formatZeroSearchResult(emptyResult)).toBe(
        'No local session events matched "missing". Searched 1 session.'
      );
    } finally {
      rmSync(rootDir, { recursive: true, force: true });
    }
  });

  it('redacts secrets from formatted search output', async () => {
    const rootDir = tempRoot();
    try {
      const store = new ZeroSessionEventStore({
        rootDir,
        now: sequenceClock([
          '2026-06-03T00:00:00.000Z',
          '2026-06-03T00:00:01.000Z',
        ]),
      });
      await store.createSession({
        sessionId: 'secret_search',
        title: 'Secret Search sk-proj-abcdefghijklmnopqrstuvwxyz1234567890',
        cwd: '/repo/zero?token=local-cwd-secret',
      });
      await store.appendEvent('secret_search', {
        type: 'tool_result',
        payload: {
          result:
            'diagnostic output includes OPENAI_API_KEY=sk-proj-abcdefghijklmnopqrstuvwxyz1234567890',
          authorization: 'Bearer local-authorization-secret',
          headers: {
            'x-api-key': 'local-header-secret',
          },
        },
      });

      const result = await searchZeroSessions('diagnostic output', {
        store,
        contextChars: 240,
      });
      const output = formatZeroSearchResult(result);

      expect(output).toContain(ZERO_REDACTED_SECRET);
      expect(output).not.toContain('sk-proj-abcdefghijklmnopqrstuvwxyz1234567890');
      expect(output).not.toContain('local-cwd-secret');
      expect(output).not.toContain('local-authorization-secret');
      expect(output).not.toContain('local-header-secret');
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
