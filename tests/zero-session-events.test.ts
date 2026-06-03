import { describe, expect, it } from 'bun:test';
import { mkdtempSync, rmSync } from 'fs';
import { readFile } from 'fs/promises';
import { tmpdir } from 'os';
import { join } from 'path';
import {
  ZeroSessionEventStore,
  defaultZeroSessionRoot,
} from '../src/zero-sessions';

function tempRoot(): string {
  return mkdtempSync(join(tmpdir(), 'zero-sessions-'));
}

describe('ZeroSessionEventStore', () => {
  it('creates session metadata and appends ordered JSONL events', async () => {
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

      const session = await store.createSession({
        sessionId: 'session_abc123',
        title: 'Implement event store',
        cwd: '/repo/zero',
        modelId: 'gpt-4.1',
        provider: 'openai',
      });
      expect(session).toEqual({
        sessionId: 'session_abc123',
        title: 'Implement event store',
        cwd: '/repo/zero',
        modelId: 'gpt-4.1',
        provider: 'openai',
        createdAt: '2026-06-03T00:00:00.000Z',
        updatedAt: '2026-06-03T00:00:00.000Z',
        eventCount: 0,
      });

      const first = await store.appendEvent('session_abc123', {
        type: 'message',
        payload: { role: 'user', content: 'List files' },
      });
      const second = await store.appendEvent('session_abc123', {
        type: 'provider_usage',
        payload: { promptTokens: 12, completionTokens: 8 },
      });

      expect(first.sequence).toBe(1);
      expect(second.sequence).toBe(2);
      expect(first.id).toBe('session_abc123:1');
      expect(second.id).toBe('session_abc123:2');

      const events = await store.readEvents('session_abc123');
      expect(events).toEqual([first, second]);

      const eventsFile = await readFile(
        join(rootDir, 'session_abc123', 'events.jsonl'),
        'utf-8'
      );
      expect(eventsFile.trim().split('\n')).toHaveLength(2);

      const updated = await store.getSession('session_abc123');
      expect(updated?.updatedAt).toBe('2026-06-03T00:00:02.000Z');
      expect(updated?.eventCount).toBe(2);
      expect(updated?.lastEventType).toBe('provider_usage');
    } finally {
      rmSync(rootDir, { recursive: true, force: true });
    }
  });

  it('lists sessions by most recently updated first', async () => {
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

      await store.createSession({ sessionId: 'older', title: 'Older' });
      await store.createSession({ sessionId: 'newer', title: 'Newer' });
      await store.appendEvent('older', {
        type: 'message',
        payload: { role: 'assistant', content: 'updated later' },
      });

      const sessions = await store.listSessions();
      expect(sessions.map((session) => session.sessionId)).toEqual([
        'older',
        'newer',
      ]);
      expect(sessions[0]?.eventCount).toBe(1);
      expect(sessions[1]?.eventCount).toBe(0);
    } finally {
      rmSync(rootDir, { recursive: true, force: true });
    }
  });

  it('does not overwrite an existing session', async () => {
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

      await store.createSession({ sessionId: 'duplicate', title: 'Original' });
      const event = await store.appendEvent('duplicate', {
        type: 'message',
        payload: { role: 'user', content: 'keep this event' },
      });

      await expect(
        store.createSession({ sessionId: 'duplicate', title: 'Replacement' })
      ).rejects.toThrow('Zero session already exists: duplicate');

      expect(await store.getSession('duplicate')).toMatchObject({
        title: 'Original',
        eventCount: 1,
      });
      expect(await store.readEvents('duplicate')).toEqual([event]);
    } finally {
      rmSync(rootDir, { recursive: true, force: true });
    }
  });

  it('searches persisted event payload text with bounded context', async () => {
    const rootDir = tempRoot();
    try {
      const store = new ZeroSessionEventStore({
        rootDir,
        now: sequenceClock([
          '2026-06-03T00:00:00.000Z',
          '2026-06-03T00:00:01.000Z',
        ]),
      });

      await store.createSession({ sessionId: 'searchable' });
      await store.appendEvent('searchable', {
        type: 'tool_result',
        payload: {
          toolCallId: 'call_1',
          result: 'The Zero event store writes append-only JSONL files.',
        },
      });

      const hits = await store.searchEvents('jsonl', { contextChars: 12 });
      expect(hits).toEqual([
        {
          sessionId: 'searchable',
          eventId: 'searchable:1',
          sequence: 1,
          type: 'tool_result',
          context: 'append-only JSONL files.',
        },
      ]);
    } finally {
      rmSync(rootDir, { recursive: true, force: true });
    }
  });

  it('limits event search results for callers that only need the first matches', async () => {
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

      await store.createSession({ sessionId: 'limited' });
      await store.appendEvent('limited', {
        type: 'message',
        payload: { content: 'first matching zero event' },
      });
      await store.appendEvent('limited', {
        type: 'message',
        payload: { content: 'second matching zero event' },
      });

      const hits = await store.searchEvents('matching', { limit: 1 });
      expect(hits).toHaveLength(1);
      expect(hits[0]?.eventId).toBe('limited:1');
    } finally {
      rmSync(rootDir, { recursive: true, force: true });
    }
  });

  it('rejects invalid session ids and corrupted event lines', async () => {
    const rootDir = tempRoot();
    try {
      const store = new ZeroSessionEventStore({ rootDir });

      await expect(store.createSession({ sessionId: '../bad' })).rejects.toThrow(
        'Invalid Zero session id'
      );

      await store.createSession({ sessionId: 'valid_session' });
      await Bun.write(
        join(rootDir, 'valid_session', 'events.jsonl'),
        '{"type":"message"}\nnot json\n'
      );

      await expect(store.readEvents('valid_session')).rejects.toThrow(
        'Invalid JSON in Zero session valid_session events.jsonl at line 2'
      );
    } finally {
      rmSync(rootDir, { recursive: true, force: true });
    }
  });
});

describe('defaultZeroSessionRoot', () => {
  it('uses XDG_DATA_HOME when present and falls back to ~/.local/share', () => {
    expect(defaultZeroSessionRoot({
      env: { XDG_DATA_HOME: '/xdg/data', HOME: '/home/zero' },
    })).toBe(join('/xdg/data', 'zero', 'sessions'));

    expect(defaultZeroSessionRoot({
      env: { HOME: '/home/zero' },
    })).toBe(join('/home/zero', '.local', 'share', 'zero', 'sessions'));
  });
});

function sequenceClock(values: string[]): () => Date {
  let index = 0;
  return () => new Date(values[Math.min(index++, values.length - 1)]!);
}
