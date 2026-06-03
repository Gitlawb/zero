import { existsSync, readFileSync } from 'fs';
import { join } from 'path';
import { homedir } from 'os';
import {
  mergeLayers,
  ZeroConfigSchema,
  type ZeroConfig,
} from '../config/loader';
import {
  parseProviderProfileKind,
  type ProviderProfileKind,
} from '../config/types';
import { ZERO_DEFAULT_MODEL_ID } from '../zero-model-registry';
import {
  redactZeroErrorMessage,
  redactZeroSecrets,
  redactZeroString,
} from '../zero-redaction';
import type {
  ZeroConfigInspectionOptions,
  ZeroConfigInspectionReport,
  ZeroConfigIssue,
  ZeroConfigLayerInspection,
  ZeroConfigLayerSource,
  ZeroConfigProviderInspection,
} from './types';

const DEFAULT_CONFIG: ZeroConfig = {
  providers: [],
  maxTurns: 12,
  planMode: false,
  debug: false,
};

export function inspectZeroConfig(
  options: ZeroConfigInspectionOptions = {}
): ZeroConfigInspectionReport {
  const now = options.now ?? (() => new Date());
  const env = options.env ?? process.env;
  const userConfigPath = options.userConfigPath ?? defaultUserConfigPath();
  const projectConfigPath = options.projectConfigPath ?? defaultProjectConfigPath();

  const layers: ZeroConfigLayerInspection[] = [
    {
      source: 'defaults',
      status: 'loaded',
      present: true,
      config: DEFAULT_CONFIG,
    },
  ];
  const issues: ZeroConfigIssue[] = [];

  const userLayer = readConfigLayer('user', userConfigPath);
  if (userLayer) {
    layers.push(userLayer);
    collectLayerIssues(userLayer, issues);
  }

  const projectLayer = readConfigLayer('project', projectConfigPath);
  if (projectLayer) {
    layers.push(projectLayer);
    collectLayerIssues(projectLayer, issues);
  }

  const envConfig = inspectEnvConfig(env, issues);
  if (Object.keys(envConfig).length > 0 || hasProviderEnv(env)) {
    layers.push({
      source: 'env',
      status: 'loaded',
      present: true,
      config: envConfig,
    });
  }

  if (options.cliOverrides && Object.keys(options.cliOverrides).length > 0) {
    layers.push({
      source: 'cli',
      status: 'loaded',
      present: true,
      config: options.cliOverrides,
    });
  }

  const effective = mergeLayers(
    DEFAULT_CONFIG,
    ...(layers
      .filter((layer) => layer.status === 'loaded' && layer.source !== 'defaults')
      .map((layer) => layer.config) as ZeroConfig[])
  );
  const provider = inspectProviderConfig(effective, env, issues);

  if (
    effective.activeProvider &&
    !effective.providers?.some((providerProfile) => providerProfile.name === effective.activeProvider)
  ) {
    issues.push({
      id: 'config.activeProvider.missing',
      severity: 'error',
      source: 'provider',
      message: `Active provider "${effective.activeProvider}" is not defined in providers.`,
    });
  }

  return redactZeroSecrets({
    generatedAt: now().toISOString(),
    ok: !issues.some((issue) => issue.severity === 'error'),
    effective,
    layers,
    provider,
    issues,
  }) as ZeroConfigInspectionReport;
}

export function formatZeroConfigInspection(report: ZeroConfigInspectionReport): string {
  const lines = [
    `Zero config inspection (${redactZeroString(report.generatedAt)})`,
    `Overall: ${report.ok ? 'pass' : 'fail'}`,
    'Layers:',
  ];

  for (const layer of report.layers) {
    const location = layer.path ? ` ${redactZeroString(layer.path)}` : '';
    lines.push(`  [${layer.status}] ${redactZeroString(layer.source)}${location}`);
    const layerSummary = formatLayerConfig(layer.config);
    if (layerSummary) lines.push(`    ${layerSummary}`);
    for (const error of layer.errors ?? []) {
      lines.push(`    error: ${redactZeroString(error)}`);
    }
  }

  lines.push('Effective:');
  lines.push(`  maxTurns: ${String(report.effective.maxTurns ?? 'unset')}`);
  lines.push(`  planMode: ${String(report.effective.planMode ?? false)}`);
  lines.push(`  debug: ${String(report.effective.debug ?? false)}`);
  lines.push(`  activeProvider: ${redactZeroString(report.effective.activeProvider ?? 'none')}`);
  lines.push(`  providers: ${report.effective.providers?.length ?? 0}`);
  lines.push(`provider: ${formatProvider(report.provider)}`);

  if (report.issues.length > 0) {
    lines.push('Issues:');
    for (const issue of report.issues) {
      lines.push(
        `  [${issue.severity}] ${redactZeroString(issue.id)} - ${redactZeroString(issue.message)}`
      );
    }
  }

  return lines.join('\n');
}

function readConfigLayer(
  source: Extract<ZeroConfigLayerSource, 'user' | 'project'>,
  path: string
): ZeroConfigLayerInspection | undefined {
  if (!existsSync(path)) return undefined;

  try {
    const parsed = JSON.parse(readFileSync(path, 'utf-8'));
    const result = ZeroConfigSchema.partial().safeParse(parsed);
    if (!result.success) {
      return {
        source,
        status: 'invalid',
        present: true,
        path,
        config: {},
        errors: result.error.issues.map((issue) => issue.message),
      };
    }

    return {
      source,
      status: 'loaded',
      present: true,
      path,
      config: result.data,
    };
  } catch (err: unknown) {
    return {
      source,
      status: 'invalid',
      present: true,
      path,
      config: {},
      errors: [redactZeroErrorMessage(err)],
    };
  }
}

function collectLayerIssues(
  layer: ZeroConfigLayerInspection,
  issues: ZeroConfigIssue[]
): void {
  if (layer.status !== 'invalid') return;

  issues.push({
    id: `config.${layer.source}.invalid`,
    severity: 'error',
    source: layer.source,
    path: layer.path,
    message: `${layer.source} config is invalid: ${(layer.errors ?? []).join('; ')}`,
  });
}

function inspectEnvConfig(
  env: NodeJS.ProcessEnv,
  issues: ZeroConfigIssue[]
): ZeroConfig {
  const config: ZeroConfig = {};

  if (env.ZERO_MAX_TURNS) {
    const maxTurns = Number.parseInt(env.ZERO_MAX_TURNS, 10);
    if (Number.isFinite(maxTurns)) {
      config.maxTurns = maxTurns;
    } else {
      issues.push({
        id: 'config.env.ZERO_MAX_TURNS.invalid',
        severity: 'error',
        source: 'env',
        message: `ZERO_MAX_TURNS must be an integer, received "${env.ZERO_MAX_TURNS}".`,
      });
    }
  }

  if (env.ZERO_PLAN_MODE === '1' || env.ZERO_PLAN_MODE === 'true') {
    config.planMode = true;
  }
  if (env.ZERO_DEBUG === '1' || env.ZERO_DEBUG === 'true') {
    config.debug = true;
  }

  if (env.ZERO_PROVIDER) {
    try {
      parseProviderProfileKind(env.ZERO_PROVIDER);
    } catch (err: unknown) {
      issues.push({
        id: 'config.env.ZERO_PROVIDER.invalid',
        severity: 'error',
        source: 'env',
        message: redactZeroErrorMessage(err),
      });
    }
  }

  return config;
}

function inspectProviderConfig(
  effective: ZeroConfig,
  env: NodeJS.ProcessEnv,
  issues: ZeroConfigIssue[]
): ZeroConfigProviderInspection {
  if (env.ZERO_PROVIDER_COMMAND) {
    return {
      configured: true,
      source: 'provider-command',
      commandConfigured: true,
    };
  }

  if (effective.activeProvider) {
    const activeProfile = effective.providers?.find(
      (provider) => provider.name === effective.activeProvider
    );
    if (activeProfile) {
      return {
        configured: true,
        source: 'profile',
        profileName: activeProfile.name,
        provider: activeProfile.provider ?? 'auto',
        baseURL: activeProfile.baseURL,
        model: activeProfile.model,
        apiKey: activeProfile.apiKey,
        activeProfile,
      };
    }
  }

  if (hasProviderEnv(env)) {
    let provider: ProviderProfileKind | 'auto' = 'auto';
    if (env.ZERO_PROVIDER) {
      try {
        provider = parseProviderProfileKind(env.ZERO_PROVIDER) ?? 'auto';
      } catch {
        provider = 'auto';
      }
    }

    return {
      configured: true,
      source: 'environment',
      provider,
      baseURL: env.OPENAI_BASE_URL || 'https://api.openai.com/v1',
      model: env.OPENAI_MODEL || ZERO_DEFAULT_MODEL_ID,
      apiKey: env.OPENAI_API_KEY,
    };
  }

  if ((effective.providers?.length ?? 0) > 0) {
    issues.push({
      id: 'config.provider.noActiveProvider',
      severity: 'warning',
      source: 'provider',
      message: 'Provider profiles exist, but activeProvider is not set.',
    });
  }

  return {
    configured: false,
    source: 'none',
  };
}

function hasProviderEnv(env: NodeJS.ProcessEnv): boolean {
  return Boolean(
    env.OPENAI_API_KEY ||
    env.OPENAI_BASE_URL ||
    env.OPENAI_MODEL ||
    env.ZERO_PROVIDER ||
    env.ZERO_PROVIDER_COMMAND
  );
}

function formatLayerConfig(config: Partial<ZeroConfig>): string {
  const parts = [
    config.maxTurns !== undefined ? `maxTurns=${config.maxTurns}` : undefined,
    config.planMode !== undefined ? `planMode=${config.planMode}` : undefined,
    config.debug !== undefined ? `debug=${config.debug}` : undefined,
    config.activeProvider ? `activeProvider=${redactZeroString(config.activeProvider)}` : undefined,
    config.providers ? `providers=${config.providers.length}` : undefined,
  ].filter(Boolean);

  return parts.join(' | ');
}

function formatProvider(provider: ZeroConfigProviderInspection): string {
  if (!provider.configured) return 'none';

  const parts = [
    provider.source,
    provider.profileName,
    provider.provider && provider.provider !== 'auto' ? provider.provider : undefined,
    provider.model,
  ].filter(Boolean);

  return redactZeroString(parts.join(' '));
}

function defaultUserConfigPath(): string {
  return join(homedir(), '.config', 'zero', 'config.json');
}

function defaultProjectConfigPath(): string {
  return join(process.cwd(), '.zero', 'config.json');
}
