import { readFile, writeFile } from 'fs/promises';
import { join } from 'path';
import { redactZeroSecrets } from '../zero-redaction';
import type {
  ZeroSessionEvent,
  ZeroSessionEventStore,
  ZeroSessionEventType,
  ZeroSessionMetadata,
} from '../zero-sessions';

export const ZERO_SEARCH_INDEX_FILE = 'search-index.json';
export const ZERO_SEARCH_INDEX_SCHEMA_VERSION = 1;

export interface ZeroSearchIndexEntry {
  sessionId: string;
  eventId: string;
  sequence: number;
  type: ZeroSessionEventType;
  createdAt: string;
  text: string;
}

export interface ZeroSearchIndexFile {
  schemaVersion: typeof ZERO_SEARCH_INDEX_SCHEMA_VERSION;
  sessionId: string;
  sessionUpdatedAt: string;
  sessionEventCount: number;
  generatedAt: string;
  entries: ZeroSearchIndexEntry[];
}

export interface LoadZeroSearchIndexOptions {
  reindex?: boolean;
}

export class ZeroSessionSearchIndex {
  constructor(
    private readonly store: ZeroSessionEventStore,
    private readonly now: () => Date = () => new Date()
  ) {}

  async loadSession(
    session: ZeroSessionMetadata,
    options: LoadZeroSearchIndexOptions = {}
  ): Promise<ZeroSearchIndexFile> {
    if (!options.reindex) {
      const cached = await this.readSessionIndex(session);
      if (cached && isCurrentIndex(cached, session)) return cached;
    }

    return this.rebuildSession(session);
  }

  async rebuildSession(session: ZeroSessionMetadata): Promise<ZeroSearchIndexFile> {
    const events = await this.store.readEvents(session.sessionId);
    const indexFile: ZeroSearchIndexFile = {
      schemaVersion: ZERO_SEARCH_INDEX_SCHEMA_VERSION,
      sessionId: session.sessionId,
      sessionUpdatedAt: session.updatedAt,
      sessionEventCount: session.eventCount,
      generatedAt: this.now().toISOString(),
      entries: events.map((event) => toSearchIndexEntry(session.sessionId, event)),
    };

    await writeFile(
      this.indexPath(session.sessionId),
      `${JSON.stringify(indexFile, null, 2)}\n`,
      'utf-8'
    );

    return indexFile;
  }

  private async readSessionIndex(
    session: ZeroSessionMetadata
  ): Promise<ZeroSearchIndexFile | undefined> {
    try {
      const content = await readFile(this.indexPath(session.sessionId), 'utf-8');
      const parsed = JSON.parse(content);
      return isSearchIndexFile(parsed) ? parsed : undefined;
    } catch {
      return undefined;
    }
  }

  private indexPath(sessionId: string): string {
    return join(this.store.rootDir, sessionId, ZERO_SEARCH_INDEX_FILE);
  }
}

export function extractZeroSearchText(value: unknown): string {
  if (typeof value === 'string') return value;
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  if (Array.isArray(value)) {
    return value.map(extractZeroSearchText).filter(Boolean).join(' ');
  }
  if (typeof value === 'object' && value !== null) {
    return Object.values(value).map(extractZeroSearchText).filter(Boolean).join(' ');
  }
  return '';
}

function toSearchIndexEntry(
  sessionId: string,
  event: ZeroSessionEvent
): ZeroSearchIndexEntry {
  return {
    sessionId,
    eventId: event.id,
    sequence: event.sequence,
    type: event.type,
    createdAt: event.createdAt,
    text: extractZeroSearchText(redactZeroSecrets(event.payload)),
  };
}

function isCurrentIndex(
  indexFile: ZeroSearchIndexFile,
  session: ZeroSessionMetadata
): boolean {
  return (
    indexFile.schemaVersion === ZERO_SEARCH_INDEX_SCHEMA_VERSION &&
    indexFile.sessionId === session.sessionId &&
    indexFile.sessionUpdatedAt === session.updatedAt &&
    indexFile.sessionEventCount === session.eventCount &&
    indexFile.entries.length === session.eventCount &&
    indexFile.entries.every((entry) => entry.sessionId === session.sessionId)
  );
}

function isSearchIndexFile(value: unknown): value is ZeroSearchIndexFile {
  if (typeof value !== 'object' || value === null) return false;
  const candidate = value as Partial<ZeroSearchIndexFile>;
  return (
    candidate.schemaVersion === ZERO_SEARCH_INDEX_SCHEMA_VERSION &&
    typeof candidate.sessionId === 'string' &&
    typeof candidate.sessionUpdatedAt === 'string' &&
    typeof candidate.sessionEventCount === 'number' &&
    typeof candidate.generatedAt === 'string' &&
    Array.isArray(candidate.entries) &&
    candidate.entries.every(isSearchIndexEntry)
  );
}

function isSearchIndexEntry(value: unknown): value is ZeroSearchIndexEntry {
  if (typeof value !== 'object' || value === null) return false;
  const candidate = value as Partial<ZeroSearchIndexEntry>;
  return (
    typeof candidate.sessionId === 'string' &&
    typeof candidate.eventId === 'string' &&
    typeof candidate.sequence === 'number' &&
    typeof candidate.type === 'string' &&
    typeof candidate.createdAt === 'string' &&
    typeof candidate.text === 'string'
  );
}
