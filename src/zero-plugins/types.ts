export type ZeroPluginSource = 'user' | 'project' | 'custom';
export type ZeroPluginDiagnosticKind = 'io' | 'json' | 'schema' | 'duplicate';
export type ZeroPluginToolPermission = 'allow' | 'prompt' | 'deny';
export type ZeroPluginHookEvent = 'beforeTool' | 'afterTool' | 'sessionStart' | 'sessionEnd';

export interface ZeroPluginRoot {
  source: ZeroPluginSource;
  path: string;
}

export interface ZeroPluginDiagnostic {
  kind: ZeroPluginDiagnosticKind;
  message: string;
  source?: ZeroPluginSource;
  root?: string;
  pluginPath?: string;
  manifestPath?: string;
  fieldPath?: string;
  pluginId?: string;
}

export interface ZeroPluginToolExtension {
  name: string;
  description?: string;
  command: string;
  args: string[];
  inputSchema: Record<string, unknown>;
  permission: ZeroPluginToolPermission;
}

export interface ZeroPluginPromptExtension {
  name: string;
  description?: string;
  path: string;
}

export interface ZeroPluginSkillExtension {
  name: string;
  description?: string;
  path: string;
}

export interface ZeroPluginHookExtension {
  name: string;
  description?: string;
  event: ZeroPluginHookEvent;
  command: string;
  args: string[];
}

export interface ZeroPluginManifest {
  schemaVersion: 1;
  id: string;
  name: string;
  version: string;
  description?: string;
  enabled: boolean;
  tools: ZeroPluginToolExtension[];
  prompts: ZeroPluginPromptExtension[];
  skills: ZeroPluginSkillExtension[];
  hooks: ZeroPluginHookExtension[];
}

export interface ZeroLoadedPlugin extends ZeroPluginManifest {
  source: ZeroPluginSource;
  root: string;
  pluginDir: string;
  manifestPath: string;
}

export interface ZeroPluginLoadResult {
  roots: ZeroPluginRoot[];
  plugins: ZeroLoadedPlugin[];
  diagnostics: ZeroPluginDiagnostic[];
}
