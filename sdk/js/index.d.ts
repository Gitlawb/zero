export interface ZeroClientOptions {
  baseUrl: string
  token?: string
  fetch?: typeof fetch
}

export type JSONRecord = Record<string, unknown>

export interface ZeroErrorBody {
  error: {
    code: string
    message: string
  }
}

export class ZeroAPIError extends Error {
  status?: number
  code?: string
  body?: unknown
}

export interface PromptRequest {
  content: string
  model?: string
  reasoningEffort?: string
  permissionMode?: 'auto' | 'ask' | 'unsafe' | 'spec-draft' | string
  autonomy?: 'low' | 'medium' | 'high' | string
  images?: Array<{ mediaType: string; data: string }>
}

export interface RunResult {
  runId: string
  sessionId: string
  finalAnswer?: string
  status: string
  exitCode: number
}

export interface HealthStatus {
  ok: boolean
  version: string
}

export interface ConfigSnapshot {
  version: string
  cwd: string
  config?: unknown
}

export interface ProviderSnapshot {
  activeProvider?: string
  model?: string
  providers?: unknown
}

export interface ModelSnapshot {
  models: unknown
}

export interface PathInfo {
  cwd: string
}

export interface SessionMetadata {
  sessionId: string
  sessionKind?: string
  title?: string
  cwd?: string
  modelId?: string
  provider?: string
  tag?: string
  depth?: number
  parentSessionId?: string
  rootSessionId?: string
  agentName?: string
  taskId?: string
  forkedFromEventId?: string
  forkedFromSequence?: number
  spawnedFromEventId?: string
  spawnedFromSequence?: number
  specId?: string
  specFilePath?: string
  specStatus?: string
  specDraftModelId?: string
  specDraftReasoning?: string
  specUserComment?: string
  specRejectReason?: string
  specSourceSessionId?: string
  specImplSessionId?: string
  createdAt: string
  updatedAt: string
  eventCount: number
  lastEventType?: string
}

export interface SessionList {
  sessions: SessionMetadata[]
}

export interface SessionEventLog {
  events: JSONRecord[]
}

export interface SessionChildren {
  children: SessionMetadata[]
}

export interface SessionLineage {
  lineage: SessionMetadata[]
}

export interface SessionTree {
  session: SessionMetadata
  children: SessionTree[]
}

export interface SessionCreate {
  sessionId?: string
  title?: string
  cwd?: string
  modelId?: string
  provider?: string
  tag?: string
}

export interface SessionUpdate {
  title: string
}

export interface PermissionDecision {
  action: string
  reason?: string
}

export interface AskAnswer {
  answers: string[]
}

export interface OkResponse {
  ok: boolean
}

export interface AbortResponse extends OkResponse {
  runId: string
}

export interface FileInfo {
  path: string
  type: 'file' | 'directory' | (string & {})
  size: number
  modTime: string
  children?: FileInfo[]
}

export interface FileContent {
  path: string
  content: string
}

export interface FindMatch {
  path: string
  line: number
  text: string
}

export interface FindMatches {
  matches: FindMatch[]
}

export interface FindFiles {
  files: string[]
}

export type ServerEvent = JSONRecord

export interface SubscribeOptions {
  sessionId?: string
  signal?: AbortSignal
  headers?: HeadersInit
}

export interface ZeroClient {
  global: {
    health(): Promise<HealthStatus>
  }
  openapi(): Promise<JSONRecord>
  config: {
    get(): Promise<ConfigSnapshot>
  }
  provider: {
    get(): Promise<ProviderSnapshot>
  }
  models: {
    list(): Promise<ModelSnapshot>
  }
  path: {
    get(): Promise<PathInfo>
  }
  vcs: {
    get(): Promise<JSONRecord>
  }
  session: {
    list(): Promise<SessionList>
    create(body?: SessionCreate): Promise<SessionMetadata>
    get(id: string): Promise<SessionMetadata>
    update(id: string, body: SessionUpdate): Promise<SessionMetadata>
    eventLog(id: string): Promise<SessionEventLog>
    children(id: string): Promise<SessionChildren>
    lineage(id: string): Promise<SessionLineage>
    tree(id: string): Promise<SessionTree>
    fork(id: string, body?: SessionCreate): Promise<SessionMetadata>
    abort(id: string): Promise<AbortResponse>
    message(id: string, body: PromptRequest): Promise<RunResult>
    promptAsync(id: string, body: PromptRequest): Promise<void>
    permission(id: string, permissionId: string, body: PermissionDecision): Promise<OkResponse>
    ask(id: string, askId: string, body: AskAnswer): Promise<OkResponse>
  }
  file: {
    get(path: string): Promise<FileInfo>
    content(path: string): Promise<FileContent>
    status(): Promise<JSONRecord>
  }
  find: {
    content(pattern: string): Promise<FindMatches>
    file(query: string): Promise<FindFiles>
  }
  event: {
    subscribe(options?: SubscribeOptions): AsyncIterable<ServerEvent>
  }
}

export function createZeroClient(options: ZeroClientOptions): ZeroClient
