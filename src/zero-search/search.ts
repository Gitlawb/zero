import { ZeroSessionEventStore } from '../zero-sessions';
import { redactZeroString } from '../zero-redaction';
import { ZeroSessionSearchIndex } from './session-index';
import type {
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
  const searchIndex = options.searchIndex ?? new ZeroSessionSearchIndex(store);
  const limit = normalizeLimit(options.limit);
  const contextChars = normalizeContextChars(options.contextChars);

  if (!normalizedQuery || limit === 0) {
    return emptySearchResult(query, normalizedQuery, store.rootDir, 0);
  }

  const sessions = await resolveSearchSessions(store, options.sessionId);
  const hits: ZeroSearchHit[] = [];
  const terms = splitSearchTerms(normalizedQuery);

  for (const session of sessions) {
    const indexedSession = await searchIndex.loadSession(session, {
      reindex: options.reindex,
    });
    for (const entry of indexedSession.entries) {
      if (options.type && entry.type !== options.type) continue;

      const match = findSearchMatch(entry.text, normalizedQuery, terms);
      if (!match) continue;

      hits.push({
        session,
        event: {
          id: entry.eventId,
          sequence: entry.sequence,
          type: entry.type,
          createdAt: entry.createdAt,
        },
        context: buildContext(entry.text, match.start, match.end, contextChars),
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
  const query = redactZeroString(result.query.trim());
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
    const sessionId = redactZeroString(hit.session.sessionId);
    const eventType = redactZeroString(hit.event.type);
    const title = hit.session.title ? ` - ${redactZeroString(hit.session.title)}` : '';
    lines.push(
      `${index + 1}. ${sessionId} #${hit.event.sequence} ${eventType}${title}`
    );
    const context = redactZeroString(hit.context).replace(/\s+/g, ' ').trim();
    if (context) lines.push(`   ${context}`);

    const details = [
      hit.session.cwd ? `cwd: ${redactZeroString(hit.session.cwd)}` : undefined,
      hit.session.modelId ? `model: ${redactZeroString(hit.session.modelId)}` : undefined,
      hit.session.provider ? `provider: ${redactZeroString(hit.session.provider)}` : undefined,
      `updated: ${redactZeroString(hit.session.updatedAt)}`,
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

function formatCount(count: number, label: string): string {
  return `${count} ${label}${count === 1 ? '' : 's'}`;
}
