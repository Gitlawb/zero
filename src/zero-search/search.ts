import { ZeroSessionEventStore } from '../zero-sessions';
import type {
  ZeroSearchCandidate,
  ZeroSearchHit,
  ZeroSearchOptions,
  ZeroSearchResult,
} from './types';

const DEFAULT_LIMIT = 20;
const DEFAULT_CONTEXT_CHARS = 80;

export async function searchZeroSessions(
  query: string,
  options: ZeroSearchOptions = {}
): Promise<ZeroSearchResult> {
  const normalizedQuery = normalizeZeroSearchQuery(query);
  const store = options.store ?? new ZeroSessionEventStore({ rootDir: options.rootDir });
  const limit = normalizeLimit(options.limit);
  const contextChars = normalizeContextChars(options.contextChars);

  if (!normalizedQuery || limit === 0) {
    return emptySearchResult(query, normalizedQuery, store.rootDir, 0);
  }

  const sessions = await resolveSearchSessions(store, options.sessionId);
  const hits: ZeroSearchHit[] = [];
  const terms = splitSearchTerms(normalizedQuery);

  for (const session of sessions) {
    const events = await store.readEvents(session.sessionId);
    for (const event of events) {
      if (options.type && event.type !== options.type) continue;

      const candidate: ZeroSearchCandidate = {
        session,
        event,
        text: extractSearchText(event.payload),
      };
      const match = findSearchMatch(candidate.text, normalizedQuery, terms);
      if (!match) continue;

      hits.push({
        session,
        event: {
          id: event.id,
          sequence: event.sequence,
          type: event.type,
          createdAt: event.createdAt,
        },
        context: buildContext(candidate.text, match.start, match.end, contextChars),
        match,
      });

      if (hits.length >= limit) {
        return {
          query,
          normalizedQuery,
          rootDir: store.rootDir,
          searchedSessions: sessions.length,
          totalHits: hits.length,
          hits,
        };
      }
    }
  }

  return {
    query,
    normalizedQuery,
    rootDir: store.rootDir,
    searchedSessions: sessions.length,
    totalHits: hits.length,
    hits,
  };
}

export function formatZeroSearchResult(result: ZeroSearchResult): string {
  const query = result.query.trim();
  if (result.hits.length === 0) {
    return `No local session events matched "${query}". Searched ${formatCount(
      result.searchedSessions,
      'session'
    )}.`;
  }

  const lines = [
    `Found ${formatCount(result.totalHits, 'local session event')} for "${query}":`,
  ];

  result.hits.forEach((hit, index) => {
    const title = hit.session.title ? ` - ${hit.session.title}` : '';
    lines.push(
      `${index + 1}. ${hit.session.sessionId} #${hit.event.sequence} ${hit.event.type}${title}`
    );
    const context = hit.context.replace(/\s+/g, ' ').trim();
    if (context) lines.push(`   ${context}`);

    const details = [
      hit.session.cwd ? `cwd: ${hit.session.cwd}` : undefined,
      hit.session.modelId ? `model: ${hit.session.modelId}` : undefined,
      hit.session.provider ? `provider: ${hit.session.provider}` : undefined,
      `updated: ${hit.session.updatedAt}`,
    ].filter(Boolean);
    lines.push(`   ${details.join(' | ')}`);
  });

  return lines.join('\n');
}

export function normalizeZeroSearchQuery(query: string): string {
  return query.replace(/\s+/g, ' ').trim().toLowerCase();
}

async function resolveSearchSessions(
  store: ZeroSessionEventStore,
  sessionId: string | undefined
) {
  if (!sessionId) return store.listSessions();

  const session = await store.getSession(sessionId);
  return session ? [session] : [];
}

function normalizeLimit(limit: number | undefined): number {
  if (limit === undefined) return DEFAULT_LIMIT;
  if (!Number.isFinite(limit)) return DEFAULT_LIMIT;
  return Math.max(0, Math.floor(limit));
}

function normalizeContextChars(contextChars: number | undefined): number {
  if (contextChars === undefined) return DEFAULT_CONTEXT_CHARS;
  if (!Number.isFinite(contextChars)) return DEFAULT_CONTEXT_CHARS;
  return Math.max(0, Math.floor(contextChars));
}

function emptySearchResult(
  query: string,
  normalizedQuery: string,
  rootDir: string,
  searchedSessions: number
): ZeroSearchResult {
  return {
    query,
    normalizedQuery,
    rootDir,
    searchedSessions,
    totalHits: 0,
    hits: [],
  };
}

function splitSearchTerms(query: string): string[] {
  return query.split(' ').filter(Boolean);
}

function findSearchMatch(
  text: string,
  normalizedQuery: string,
  terms: string[]
): { start: number; end: number } | undefined {
  const normalizedText = text.toLowerCase();
  const phraseIndex = normalizedText.indexOf(normalizedQuery);
  if (phraseIndex >= 0) {
    return {
      start: phraseIndex,
      end: phraseIndex + normalizedQuery.length,
    };
  }

  let firstMatch = Number.POSITIVE_INFINITY;
  let lastMatch = -1;
  for (const term of terms) {
    const index = normalizedText.indexOf(term);
    if (index === -1) return undefined;
    firstMatch = Math.min(firstMatch, index);
    lastMatch = Math.max(lastMatch, index + term.length);
  }

  return {
    start: firstMatch,
    end: lastMatch,
  };
}

function buildContext(text: string, start: number, end: number, contextChars: number): string {
  return text
    .slice(Math.max(0, start - contextChars), Math.min(text.length, end + contextChars))
    .trim();
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

function formatCount(count: number, label: string): string {
  return `${count} ${label}${count === 1 ? '' : 's'}`;
}
