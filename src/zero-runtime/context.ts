import { toolRegistry } from '../tools';
import {
  resolveZeroModelProfile,
  type ZeroModelDefinition,
  type ZeroReasoningEffort,
} from '../zero-model-registry';
import { loadProviderConfig } from '../config/provider';
import {
  createZeroProvider,
  resolveZeroProviderRuntime,
} from '../zero-provider-runtime';
import type { AgentPermissionMode } from '../agent/loop';
import type {
  ZeroAutonomyLevel,
  ZeroRunContext,
  ZeroRuntimeOptions,
} from './types';
import {
  ZeroRuntimeProviderError,
  ZeroRuntimeUsageError,
} from './types';

const AUTONOMY_LEVELS: readonly ZeroAutonomyLevel[] = ['low', 'medium', 'high'];
const REASONING_EFFORTS: readonly ZeroReasoningEffort[] = [
  'none',
  'minimal',
  'low',
  'medium',
  'high',
  'xhigh',
];
const REASONING_ALIASES: Record<string, ZeroReasoningEffort> = {
  off: 'none',
};

export async function createZeroRunContext(options: ZeroRuntimeOptions): Promise<ZeroRunContext> {
  const autonomy = parseZeroAutonomyLevel(options.autonomy);
  const permissionMode = resolvePermissionMode({
    surface: options.surface,
    autonomy,
    permissionMode: options.permissionMode,
    skipPermissionsUnsafe: options.skipPermissionsUnsafe,
  });
  const enabledTools = normalizeToolList(options.enabledTools, 'enabled tool');
  const disabledTools = normalizeToolList(options.disabledTools, 'disabled tool');
  validateToolFilters(enabledTools, disabledTools);

  let providerConfig;
  let runtime;
  try {
    providerConfig = await loadProviderConfig();
    const modelResolution = resolveRuntimeModel({
      configuredModel: providerConfig.model,
      model: options.model,
      modelProfile: options.modelProfile,
    });

    runtime = resolveZeroProviderRuntime({
      provider: providerConfig.provider,
      apiKey: providerConfig.apiKey,
      baseURL: providerConfig.baseURL,
      model: modelResolution.modelId,
      profileName: providerConfig.profileName,
      source: providerConfig.source,
    });

    const reasoningEffort = resolveReasoningEffort(options.reasoningEffort, runtime.model);
    const provider = createZeroProvider(runtime);

    return {
      surface: options.surface,
      providerConfig,
      runtime,
      provider,
      model: runtime.model,
      modelProfile: modelResolution.profile,
      modelId: runtime.modelId ?? runtime.requestedModel,
      modelLabel: runtime.model?.displayName ?? runtime.requestedModel,
      providerLabel: runtime.provider,
      reasoningEffort,
      autonomy,
      permissionMode,
      enabledTools,
      disabledTools,
      agentOptions: {
        maxTurns: options.maxTurns,
        permissionMode,
        enabledTools,
        disabledTools,
        reasoningEffort,
        autonomy,
      },
    };
  } catch (err: unknown) {
    if (err instanceof ZeroRuntimeUsageError) throw err;
    throw new ZeroRuntimeProviderError(err instanceof Error ? err.message : String(err), { cause: err });
  }
}

export function parseToolList(value: string | undefined): string[] | undefined {
  if (!value) return undefined;
  const tools = value.split(/[\s,]+/).map((tool) => tool.trim()).filter(Boolean);
  return tools.length > 0 ? tools : undefined;
}

export function parseZeroAutonomyLevel(value: string | undefined): ZeroAutonomyLevel {
  if (!value) return 'low';
  const normalized = value.trim().toLowerCase();
  if (AUTONOMY_LEVELS.includes(normalized as ZeroAutonomyLevel)) {
    return normalized as ZeroAutonomyLevel;
  }
  throw new ZeroRuntimeUsageError(
    `Invalid autonomy level "${value}". Expected low, medium, or high.`
  );
}

export function resolvePermissionMode(options: {
  surface: 'tui' | 'exec';
  autonomy: ZeroAutonomyLevel;
  permissionMode?: AgentPermissionMode;
  skipPermissionsUnsafe?: boolean;
}): AgentPermissionMode {
  if (options.skipPermissionsUnsafe) return 'unsafe';
  if (options.permissionMode) return options.permissionMode;
  if (options.surface === 'tui') return 'ask';
  return options.autonomy === 'high' ? 'unsafe' : 'auto';
}

export function resolveRuntimeModel(options: {
  configuredModel: string;
  model?: string;
  modelProfile?: string;
}) {
  if (options.modelProfile) {
    const resolved = resolveZeroModelProfile(options.modelProfile);
    if (!resolved) {
      throw new ZeroRuntimeUsageError(`Unknown model profile: ${options.modelProfile}`);
    }
    return {
      modelId: resolved.model.id,
      profile: resolved.profile,
    };
  }

  if (options.model) {
    const profile = resolveZeroModelProfile(options.model);
    if (profile) {
      return {
        modelId: profile.model.id,
        profile: profile.profile,
      };
    }
    return { modelId: options.model.trim() };
  }

  return { modelId: options.configuredModel };
}

export function resolveReasoningEffort(
  value: string | undefined,
  model?: ZeroModelDefinition
): ZeroReasoningEffort | undefined {
  if (!value) return undefined;
  const normalized = REASONING_ALIASES[value.trim().toLowerCase()] ?? value.trim().toLowerCase();
  if (!REASONING_EFFORTS.includes(normalized as ZeroReasoningEffort)) {
    throw new ZeroRuntimeUsageError(
      `Invalid reasoning effort "${value}". Expected ${REASONING_EFFORTS.join(', ')}.`
    );
  }
  const efforts = model?.reasoningEfforts ?? [];
  if (efforts.length > 0 && !efforts.includes(normalized as ZeroReasoningEffort)) {
    throw new ZeroRuntimeUsageError(
      `Reasoning effort "${value}" is not supported by ${model?.displayName ?? 'this model'}. ` +
      `Supported efforts: ${efforts.join(', ')}.`
    );
  }
  return normalized as ZeroReasoningEffort;
}

function normalizeToolList(
  tools: readonly string[] | undefined,
  label: string
): readonly string[] | undefined {
  if (!tools || tools.length === 0) return undefined;
  const names = Array.from(new Set(tools.map((tool) => tool.trim()).filter(Boolean)));
  for (const tool of names) {
    if (!/^[a-zA-Z0-9_-]+$/.test(tool)) {
      throw new ZeroRuntimeUsageError(`Invalid ${label} "${tool}".`);
    }
  }
  return names;
}

function validateToolFilters(
  enabledTools: readonly string[] | undefined,
  disabledTools: readonly string[] | undefined
): void {
  const knownTools = new Set(toolRegistry.getAll().map((tool) => tool.name));
  for (const name of [...(enabledTools ?? []), ...(disabledTools ?? [])]) {
    if (!knownTools.has(name)) {
      throw new ZeroRuntimeUsageError(`Unknown tool: ${name}`);
    }
  }

  if (enabledTools && disabledTools) {
    const disabled = new Set(disabledTools);
    const overlap = enabledTools.filter((tool) => disabled.has(tool));
    if (overlap.length > 0) {
      throw new ZeroRuntimeUsageError(
        `Tool cannot be both enabled and disabled: ${overlap.join(', ')}`
      );
    }
  }
}
