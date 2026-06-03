export type ZeroSessionEventType =
  | 'message'
  | 'tool_call'
  | 'tool_result'
  | 'provider_usage'
  | 'error'
  | (string & {});

export interface ZeroSessionMetadata {
  sessionId: string;
  title?: string;
  cwd?: string;
  modelId?: string;
  provider?: string;
  createdAt: string;
  updatedAt: string;
  eventCount: number;
  lastEventType?: ZeroSessionEventType;
}

export interface CreateZeroSessionInput {
  sessionId?: string;
  title?: string;
  cwd?: string;
  modelId?: string;
  provider?: string;
}

export interface AppendZeroSessionEventInput {
  type: ZeroSessionEventType;
  payload?: unknown;
}

export interface ZeroSessionEvent {
  id: string;
  sessionId: string;
  sequence: number;
  type: ZeroSessionEventType;
  createdAt: string;
  payload?: unknown;
}

export interface ZeroSessionSearchOptions {
  contextChars?: number;
  limit?: number;
}

export interface ZeroSessionSearchHit {
  sessionId: string;
  eventId: string;
  sequence: number;
  type: ZeroSessionEventType;
  context: string;
}

export interface ZeroSessionEventStoreOptions {
  rootDir?: string;
  now?: () => Date;
}

export interface DefaultZeroSessionRootOptions {
  env?: Record<string, string | undefined>;
}
