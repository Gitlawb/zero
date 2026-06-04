export type ZeroHookEvent = 'beforeTool' | 'afterTool' | 'sessionStart' | 'sessionEnd';
export type ZeroHookDiagnosticKind = 'io' | 'json' | 'schema' | 'duplicate';
export type ZeroHookConfigSource = 'user' | 'project';

export interface ZeroHookCommand {
  command: string;
  args: string[];
}

export interface ZeroHookDefinition extends ZeroHookCommand {
  id: string;
  name?: string;
  description?: string;
  event: ZeroHookEvent;
  matcher?: string;
  enabled: boolean;
}

export interface ZeroHooksConfig {
  enabled: boolean;
  hooks: ZeroHookDefinition[];
}

export interface ZeroHookDiagnostic {
  kind: ZeroHookDiagnosticKind;
  message: string;
  source?: ZeroHookConfigSource;
  path?: string;
  hookId?: string;
  fieldPath?: string;
}

export interface ZeroHookLoadResult {
  config: ZeroHooksConfig;
  diagnostics: ZeroHookDiagnostic[];
  paths: ZeroHookPaths;
}

export interface ZeroHookPaths {
  userConfigPath: string;
  projectConfigPath: string;
  auditPath: string;
}

export interface SelectZeroHooksInput {
  event: ZeroHookEvent;
  toolName?: string;
}

export interface ZeroHookAuditCommand extends ZeroHookCommand {}

export interface ZeroHookAuditResult {
  exitCode: number;
  stdout?: string;
  stderr?: string;
}

export interface ZeroHookAuditBase {
  sequence: number;
  createdAt: string;
  hookId: string;
  event: ZeroHookEvent;
  matcher?: string;
  toolCallId?: string;
}

export interface ZeroHookExecutionStartedAudit extends ZeroHookAuditBase {
  type: 'hook_execution_started';
  commands: ZeroHookAuditCommand[];
}

export interface ZeroHookExecutionCompletedAudit extends ZeroHookAuditBase {
  type: 'hook_execution_completed';
  status: 'completed' | 'error' | 'blocked';
  results?: ZeroHookAuditResult[];
  durationMs?: number;
}

export type ZeroHookAuditEvent =
  | ZeroHookExecutionStartedAudit
  | ZeroHookExecutionCompletedAudit;

export type AppendZeroHookStartedInput = Omit<
  ZeroHookExecutionStartedAudit,
  'type' | 'sequence' | 'createdAt'
>;

export type AppendZeroHookCompletedInput = Omit<
  ZeroHookExecutionCompletedAudit,
  'type' | 'sequence' | 'createdAt'
>;
