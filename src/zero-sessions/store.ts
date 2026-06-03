import { appendFile, mkdir, readFile, readdir, writeFile } from 'fs/promises';
import { homedir } from 'os';
import { join } from 'path';
import type {
  AppendZeroSessionEventInput,
  CreateZeroSessionInput,
  DefaultZeroSessionRootOptions,
  ZeroSessionEvent,
  ZeroSessionEventStoreOptions,
  ZeroSessionMetadata,
  ZeroSessionSearchHit,
  ZeroSessionSearchOptions,
} from './types';

const SESSION_ID_PATTERN = /^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/;
const METADATA_FILE = 'metadata.json';
const EVENTS_FILE = 'events.jsonl';

export function defaultZeroSessionRoot(options: DefaultZeroSessionRootOptions = {}): string {
  const env = options.env ?? process.env;
  const dataHome = env.XDG_DATA_HOME?.trim();
  const home = env.HOME?.trim() || homedir();
  const baseDir = dataHome || join(home, '.local', 'share');

  return join(baseDir, 'zero', 'sessions');
}

export class ZeroSessionEventStore {
  readonly rootDir: string;

  private readonly now: () => Date;

  constructor(options: ZeroSessionEventStoreOptions = {}) {
    this.rootDir = options.rootDir ?? defaultZeroSessionRoot();
    this.now = options.now ?? (() => new Date());
  }

  async createSession(input: CreateZeroSessionInput = {}): Promise<ZeroSessionMetadata> {
    const sessionId = input.sessionId ?? createZeroSessionId(this.now());
    assertValidSessionId(sessionId);

    const createdAt = this.timestamp();
    const session: ZeroSessionMetadata = {
      sessionId,
      title: input.title,
      cwd: input.cwd,
      modelId: input.modelId,
      provider: input.provider,
      createdAt,
      updatedAt: createdAt,
      eventCount: 0,
    };

    await mkdir(this.rootDir, { recursive: true });
    await mkdir(this.sessionPath(sessionId)).catch((err: unknown) => {
      if (isFileExistsError(err)) {
        throw new Error(`Zero session already exists: ${sessionId}`);
      }
      throw err;
    });
    await this.writeMetadata(session);
    await writeFile(this.eventsPath(sessionId), '', { flag: 'wx' }).catch((err: unknown) => {
      if (isFileExistsError(err)) return;
      throw err;
    });

    return session;
  }

  async getSession(sessionId: string): Promise<ZeroSessionMetadata | undefined> {
    assertValidSessionId(sessionId);

    try {
      return await this.readMetadata(sessionId);
    } catch (err: unknown) {
      if (isNotFoundError(err)) return undefined;
      throw err;
    }
  }

  async listSessions(): Promise<ZeroSessionMetadata[]> {
    await mkdir(this.rootDir, { recursive: true });
    const entries = await readdir(this.rootDir, { withFileTypes: true });
    const sessions: ZeroSessionMetadata[] = [];

    for (const entry of entries) {
      if (!entry.isDirectory() || !isValidSessionId(entry.name)) continue;

      const session = await this.getSession(entry.name);
      if (session) sessions.push(session);
    }

    return sessions.sort((left, right) => {
      const updated = right.updatedAt.localeCompare(left.updatedAt);
      return updated || left.sessionId.localeCompare(right.sessionId);
    });
  }

  async appendEvent(
    sessionId: string,
    input: AppendZeroSessionEventInput
  ): Promise<ZeroSessionEvent> {
    assertValidSessionId(sessionId);
    const session = await this.readMetadata(sessionId);
    const sequence = session.eventCount + 1;
    const createdAt = this.timestamp();
    const event: ZeroSessionEvent = {
      id: `${sessionId}:${sequence}`,
      sessionId,
      sequence,
      type: input.type,
      createdAt,
      payload: input.payload,
    };

    await appendFile(this.eventsPath(sessionId), `${JSON.stringify(event)}\n`, 'utf-8');
    await this.writeMetadata({
      ...session,
      updatedAt: createdAt,
      eventCount: sequence,
      lastEventType: input.type,
    });

    return event;
  }

  async readEvents(sessionId: string): Promise<ZeroSessionEvent[]> {
    assertValidSessionId(sessionId);

    let content: string;
    try {
      content = await readFile(this.eventsPath(sessionId), 'utf-8');
    } catch (err: unknown) {
      if (isNotFoundError(err)) return [];
      throw err;
    }

    const events: ZeroSessionEvent[] = [];
    const lines = content.split('\n');

    for (let index = 0; index < lines.length; index += 1) {
      const line = lines[index]?.trim();
      if (!line) continue;

      try {
        events.push(JSON.parse(line) as ZeroSessionEvent);
      } catch {
        throw new Error(
          `Invalid JSON in Zero session ${sessionId} ${EVENTS_FILE} at line ${index + 1}`
        );
      }
    }

    return events;
  }

  async searchEvents(
    query: string,
    options: ZeroSessionSearchOptions = {}
  ): Promise<ZeroSessionSearchHit[]> {
    const normalizedQuery = query.trim().toLowerCase();
    if (!normalizedQuery) return [];

    const contextChars = Math.max(0, Math.floor(options.contextChars ?? 80));
    const limit = Math.max(0, Math.floor(options.limit ?? Number.POSITIVE_INFINITY));
    const hits: ZeroSessionSearchHit[] = [];

    for (const session of await this.listSessions()) {
      for (const event of await this.readEvents(session.sessionId)) {
        const text = extractSearchText(event.payload);
        const matchIndex = text.toLowerCase().indexOf(normalizedQuery);
        if (matchIndex === -1) continue;

        hits.push({
          sessionId: session.sessionId,
          eventId: event.id,
          sequence: event.sequence,
          type: event.type,
          context: text.slice(
            Math.max(0, matchIndex - contextChars),
            Math.min(text.length, matchIndex + query.trim().length + contextChars)
          ),
        });

        if (hits.length >= limit) return hits;
      }
    }

    return hits;
  }

  private timestamp(): string {
    return this.now().toISOString();
  }

  private sessionPath(sessionId: string): string {
    return join(this.rootDir, sessionId);
  }

  private metadataPath(sessionId: string): string {
    return join(this.sessionPath(sessionId), METADATA_FILE);
  }

  private eventsPath(sessionId: string): string {
    return join(this.sessionPath(sessionId), EVENTS_FILE);
  }

  private async readMetadata(sessionId: string): Promise<ZeroSessionMetadata> {
    const content = await readFile(this.metadataPath(sessionId), 'utf-8');
    return JSON.parse(content) as ZeroSessionMetadata;
  }

  private async writeMetadata(session: ZeroSessionMetadata): Promise<void> {
    await writeFile(this.metadataPath(session.sessionId), `${JSON.stringify(session, null, 2)}\n`);
  }
}

function createZeroSessionId(now: Date): string {
  const date = now.toISOString().replace(/[-:.TZ]/g, '').slice(0, 14);
  const random = Math.random().toString(36).slice(2, 10);
  return `zero_${date}_${random}`;
}

function assertValidSessionId(sessionId: string): void {
  if (!isValidSessionId(sessionId)) {
    throw new Error('Invalid Zero session id');
  }
}

function isValidSessionId(sessionId: string): boolean {
  return SESSION_ID_PATTERN.test(sessionId);
}

function extractSearchText(value: unknown): string {
  if (typeof value === 'string') return value;
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  if (Array.isArray(value)) {
    return value.map(extractSearchText).filter(Boolean).join(' ');
  }
  if (typeof value === 'object' && value !== null) {
    return Object.values(value).map(extractSearchText).filter(Boolean).join(' ');
  }
  return '';
}

function isFileExistsError(err: unknown): boolean {
  return isNodeErrnoException(err) && err.code === 'EEXIST';
}

function isNotFoundError(err: unknown): boolean {
  return isNodeErrnoException(err) && err.code === 'ENOENT';
}

function isNodeErrnoException(err: unknown): err is NodeJS.ErrnoException {
  return err instanceof Error && 'code' in err;
}
