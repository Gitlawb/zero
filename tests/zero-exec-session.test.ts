import { describe, expect, it } from 'bun:test';
import { mkdtempSync, rmSync } from 'fs';
import { tmpdir } from 'os';
import { join } from 'path';
import {
  ZeroSessionEventStore,
  formatZeroExecSessionPrompt,
  prepareZeroExecSession,
} from '../src/zero-sessions';

function tempRoot(): string {
  return mkdtempSync(join(tmpdir(), 'zero-exec-session-'));
}

describe('Zero exec session resolution', () => {
  it('creates a new session for a normal exec run', async () => {
    const rootDir = tempRoot();
    try {
      const store = new ZeroSessionEventStore({
        rootDir,
        now: sequenceClock(['2026-06-03T10:00:00.000Z']),
      });

      const prepared = await prepareZeroExecSession({
        store,
        cwd: '/repo/zero',
        modelId: 'gpt-4.1',
        provider: 'openai',
        title: 'Implement sessions',
      });

      expect(prepared.mode).toBe('new');
      expect(prepared.session).toMatchObject({
        title: 'Implement sessions',
        cwd: '/repo/zero',
        modelId: 'gpt-4.1',
        provider: 'openai',
        eventCount: 0,
      });
      expect(prepared.contextEvents).toEqual([]);
    } finally {
      rmSync(rootDir, { recursive: true, force: true });
    }
  });

  it('resumes the latest session when --resume has no id', async () => {
    const rootDir = tempRoot();
    try {
      const store = new ZeroSessionEventStore({
        rootDir,
        now: sequenceClock([
          '2026-06-03T10:00:00.000Z',
          '2026-06-03T10:00:01.000Z',
          '2026-06-03T10:00:02.000Z',
        ]),
      });
      await store.createSession({ sessionId: 'older', title: 'Older' });
      await store.createSession({ sessionId: 'latest', title: 'Latest' });
      await store.appendEvent('latest', {
        type: 'message',
        payload: { role: 'assistant', content: 'previous answer' },
      });

      const prepared = await prepareZeroExecSession({
        store,
        resume: true,
        cwd: '/repo/zero',
        modelId: 'gpt-4.1',
        provider: 'openai',
      });

      expect(prepared.mode).toBe('resume');
      expect(prepared.session.sessionId).toBe('latest');
      expect(prepared.contextEvents.map((event) => event.id)).toEqual(['latest:1']);
      expect(formatZeroExecSessionPrompt('continue', prepared)).toContain('previous answer');
    } finally {
      rmSync(rootDir, { recursive: true, force: true });
    }
  });

  it('forks an existing session into a new lineage-preserving session', async () => {
    const rootDir = tempRoot();
    try {
      const store = new ZeroSessionEventStore({
        rootDir,
        now: sequenceClock([
          '2026-06-03T10:00:00.000Z',
          '2026-06-03T10:00:01.000Z',
          '2026-06-03T10:00:02.000Z',
          '2026-06-03T10:00:03.000Z',
          '2026-06-03T10:00:04.000Z',
          '2026-06-03T10:00:05.000Z',
        ]),
      });
      await store.createSession({
        sessionId: 'parent',
        title: 'Parent work',
        cwd: '/repo/old',
        modelId: 'gpt-4.1',
        provider: 'openai',
      });
      await store.appendEvent('parent', {
        type: 'message',
        payload: { role: 'user', content: 'original request' },
      });
      await store.appendEvent('parent', {
        type: 'tool_result',
        payload: { result: 'original tool output' },
      });

      const prepared = await prepareZeroExecSession({
        store,
        fork: 'parent',
        sessionId: 'forked',
        cwd: '/repo/new',
        modelId: 'claude-sonnet-4.5',
        provider: 'anthropic',
      });

      expect(prepared.mode).toBe('fork');
      expect(prepared.session).toMatchObject({
        sessionId: 'forked',
        parentSessionId: 'parent',
        title: 'Parent work (fork)',
        cwd: '/repo/new',
        modelId: 'claude-sonnet-4.5',
        provider: 'anthropic',
        eventCount: 3,
        lastEventType: 'session_fork',
      });
      expect(prepared.contextEvents.map((event) => event.id)).toEqual([
        'parent:1',
        'parent:2',
      ]);

      const forkedEvents = await store.readEvents('forked');
      expect(forkedEvents.map((event) => [event.id, event.type])).toEqual([
        ['forked:1', 'message'],
        ['forked:2', 'tool_result'],
        ['forked:3', 'session_fork'],
      ]);
      expect(forkedEvents[2]?.payload).toMatchObject({
        parentSessionId: 'parent',
        copiedEventCount: 2,
      });
    } finally {
      rmSync(rootDir, { recursive: true, force: true });
    }
  });

  it('rejects ambiguous or missing resume and fork targets', async () => {
    const rootDir = tempRoot();
    try {
      const store = new ZeroSessionEventStore({ rootDir });

      await expect(
        prepareZeroExecSession({ store, resume: 'abc', fork: 'def' })
      ).rejects.toThrow('Use either --resume or --fork, not both');
      await expect(
        prepareZeroExecSession({ store, resume: true })
      ).rejects.toThrow('No Zero sessions available to resume');
      await expect(
        prepareZeroExecSession({ store, resume: 'missing' })
      ).rejects.toThrow('Zero session not found: missing');
      await expect(
        prepareZeroExecSession({ store, fork: 'missing' })
      ).rejects.toThrow('Zero session not found: missing');
    } finally {
      rmSync(rootDir, { recursive: true, force: true });
    }
  });
});

function sequenceClock(values: string[]): () => Date {
  let index = 0;
  return () => new Date(values[Math.min(index++, values.length - 1)]!);
}
