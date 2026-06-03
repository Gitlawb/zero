import type {
  ZeroSessionEvent,
  ZeroSessionEventStore,
  ZeroSessionEventType,
  ZeroSessionMetadata,
} from '../zero-sessions';

export interface ZeroSearchOptions {
  store?: ZeroSessionEventStore;
  rootDir?: string;
  limit?: number;
  contextChars?: number;
  sessionId?: string;
  type?: ZeroSessionEventType;
}

export interface ZeroSearchEventSummary {
  id: string;
  sequence: number;
  type: ZeroSessionEventType;
  createdAt: string;
}

export interface ZeroSearchHit {
  session: ZeroSessionMetadata;
  event: ZeroSearchEventSummary;
  context: string;
  match: {
    start: number;
    end: number;
  };
}

export interface ZeroSearchResult {
  query: string;
  normalizedQuery: string;
  rootDir: string;
  searchedSessions: number;
  totalHits: number;
  hits: ZeroSearchHit[];
}

export interface ZeroSearchCandidate {
  session: ZeroSessionMetadata;
  event: ZeroSessionEvent;
  text: string;
}
