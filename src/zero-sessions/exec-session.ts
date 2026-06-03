import { redactZeroString } from '../zero-redaction';
import { ZeroSessionEventStore } from './store';
import type {
  CreateZeroSessionInput,
  ZeroSessionEvent,
  ZeroSessionMetadata,
} from './types';

export type ZeroExecSessionMode = 'new' | 'resume' | 'fork';

export class ZeroExecSessionError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'ZeroExecSessionError';
  }
}

export interface PrepareZeroExecSessionOptions extends CreateZeroSessionInput {
  store?: ZeroSessionEventStore;
  resume?: string | boolean;
  fork?: string;
}

export interface PreparedZeroExecSession {
  mode: ZeroExecSessionMode;
  session: ZeroSessionMetadata;
  contextEvents: ZeroSessionEvent[];
  store: ZeroSessionEventStore;
}

export async function prepareZeroExecSession(
  options: PrepareZeroExecSessionOptions = {}
): Promise<PreparedZeroExecSession> {
  if (options.resume && options.fork) {
    throw new ZeroExecSessionError('Use either --resume or --fork, not both.');
  }

  const store = options.store ?? new ZeroSessionEventStore();
  const sessionInput = toCreateSessionInput(options);

  if (options.fork) {
    const parent = await store.getSession(options.fork);
    if (!parent) throw new ZeroExecSessionError(`Zero session not found: ${options.fork}`);

    const contextEvents = await store.readEvents(parent.sessionId);
    const session = await store.forkSession(parent.sessionId, {
      ...sessionInput,
      title: sessionInput.title ?? (parent.title ? `${parent.title} (fork)` : undefined),
    });

    return {
      mode: 'fork',
      session,
      contextEvents,
      store,
    };
  }

  if (options.resume) {
    const sessionId = await resolveResumeSessionId(store, options.resume);
    const session = await store.getSession(sessionId);
    if (!session) throw new ZeroExecSessionError(`Zero session not found: ${sessionId}`);

    return {
      mode: 'resume',
      session,
      contextEvents: await store.readEvents(session.sessionId),
      store,
    };
  }

  return {
    mode: 'new',
    session: await store.createSession(sessionInput),
    contextEvents: [],
    store,
  };
}

export function formatZeroExecSessionPrompt(
  prompt: string,
  prepared: PreparedZeroExecSession,
  maxEvents = 20
): string {
  if (prepared.mode === 'new' || prepared.contextEvents.length === 0) {
    return prompt;
  }

  const recentEvents = prepared.contextEvents.slice(-Math.max(1, maxEvents));
  const context = recentEvents
    .map((event) => `- #${event.sequence} ${event.type}: ${summarizeEventPayload(event.payload)}`)
    .join('\n');

  const label = prepared.mode === 'fork' ? 'Forked from' : 'Continuing';
  return [
    `${label} Zero session ${prepared.session.parentSessionId ?? prepared.session.sessionId}.`,
    'Previous session context:',
    context,
    '',
    'Current user request:',
    prompt,
  ].join('\n');
}

async function resolveResumeSessionId(
  store: ZeroSessionEventStore,
  resume: string | boolean
): Promise<string> {
  if (typeof resume === 'string' && resume.trim()) {
    return resume.trim();
  }

  const latest = await store.getLatestSession();
  if (!latest) throw new ZeroExecSessionError('No Zero sessions available to resume.');
  return latest.sessionId;
}

function toCreateSessionInput(
  options: PrepareZeroExecSessionOptions
): CreateZeroSessionInput {
  return {
    sessionId: options.sessionId,
    title: options.title,
    cwd: options.cwd,
    modelId: options.modelId,
    provider: options.provider,
  };
}

function summarizeEventPayload(payload: unknown): string {
  const text = extractText(payload).replace(/\s+/g, ' ').trim();
  const summary = text || JSON.stringify(payload ?? {});
  return redactZeroString(summary.slice(0, 500));
}

function extractText(value: unknown): string {
  if (typeof value === 'string') return value;
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  if (Array.isArray(value)) return value.map(extractText).filter(Boolean).join(' ');
  if (typeof value === 'object' && value !== null) {
    return Object.values(value).map(extractText).filter(Boolean).join(' ');
  }
  return '';
}
