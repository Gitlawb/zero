import type {
  ZeroSessionEventStore,
  ZeroSessionEventType,
  ZeroSessionMetadata,
} from '../zero-sessions';
import type { ZeroSessionSearchIndex } from './session-index';

export interface ZeroSearchOptions {
  store?: ZeroSessionEventStore;
  searchIndex?: ZeroSessionSearchIndex;
  rootDir?: string;
  limit?: number;
  contextChars?: number;
  sessionId?: string;
  type?: ZeroSessionEventType;
  reindex?: boolean;
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
