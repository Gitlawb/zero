import { existsSync, readFileSync } from 'fs';
import { join } from 'path';
import { homedir } from 'os';
import { loadConfigWithLayers, ZeroConfigSchema } from '../config/loader';
import { loadProviderConfig, type ProviderConfig } from '../config/provider';
import {
  createZeroProvider,
  resolveZeroProviderRuntime,
} from '../zero-provider-runtime';
import type { ZeroResolvedProviderRuntime } from '../zero-provider-runtime';
import {
  redactZeroErrorMessage,
  redactZeroSecrets,
  redactZeroString,
} from '../zero-redaction';
import type {
  ZeroDoctorCheck,
  ZeroDoctorOptions,
  ZeroDoctorReport,
  ZeroDoctorStatus,
} from './types';

const DEFAULT_CONNECTIVITY_TIMEOUT_MS = 3000;

export async function runZeroDoctor(
  options: ZeroDoctorOptions = {}
): Promise<ZeroDoctorReport> {
  const checks: ZeroDoctorCheck[] = [];
  const now = options.now ?? (() => new Date());

  checks.push(checkBunRuntime(options.bunVersion));
  checks.push(checkConfigFiles(options));

  const providerConfig = await resolveDoctorProviderConfig(options);
  checks.push(providerConfig.check);

  const runtimeResult = checkProviderRuntime(providerConfig.value);
  checks.push(runtimeResult.check);
  checks.push(checkProviderAdapter(runtimeResult.runtime));
  checks.push(await checkProviderConnectivity(runtimeResult.runtime, options));

  return {
    generatedAt: now().toISOString(),
    ok: checks.every((check) => check.status !== 'fail'),
    checks,
  };
}

export function formatZeroDoctorReport(report: ZeroDoctorReport): string {
  const status = report.ok ? 'pass' : 'fail';
  const lines = [
    `Zero doctor report (${redactZeroString(report.generatedAt)})`,
    `Overall: ${status}`,
  ];

  for (const check of report.checks) {
    lines.push(
      `[${check.status}] ${redactZeroString(check.id)} - ${redactZeroString(check.message)}`
    );
    const detailLine = formatDetails(check.details);
    if (detailLine) lines.push(`  ${detailLine}`);
  }

  return lines.join('\n');
}

function checkBunRuntime(bunVersion: string | undefined): ZeroDoctorCheck {
  const version = bunVersion ?? getBunVersion();
  if (!version) {
    return check(
      'runtime.bun',
      'Bun runtime',
      'fail',
      'Bun runtime was not detected. Zero must be run with Bun for local CLI support.'
    );
  }

  return check(
    'runtime.bun',
    'Bun runtime',
    'pass',
    `Bun ${version} is available.`,
    { version }
  );
}

function checkConfigFiles(options: ZeroDoctorOptions): ZeroDoctorCheck {
  const userConfigPath = options.userConfigPath ?? defaultUserConfigPath();
  const projectConfigPath = options.projectConfigPath ?? defaultProjectConfigPath();
  const fileErrors = [
    validateConfigFile('user', userConfigPath),
    validateConfigFile('project', projectConfigPath),
  ].filter(Boolean) as string[];

  if (fileErrors.length > 0) {
    return check(
      'config.files',
      'Config files',
      'fail',
      'One or more Zero config files are invalid.',
      {
        errors: fileErrors,
        userConfigPath,
        projectConfigPath,
      }
    );
  }

  const { layers } = loadConfigWithLayers({
    userConfigPath,
    projectConfigPath,
    env: options.env,
  });

  return check(
    'config.files',
    'Config files',
    'pass',
    `Config loaded from ${layers.length} layer${layers.length === 1 ? '' : 's'}.`,
    {
      layers: layers.map((layer) => layer.source),
      userConfigPath,
      projectConfigPath,
    }
  );
}

async function resolveDoctorProviderConfig(
  options: ZeroDoctorOptions
): Promise<{ check: ZeroDoctorCheck; value?: ProviderConfig }> {
  try {
    const providerConfig =
      'providerConfig' in options
        ? options.providerConfig ?? undefined
        : await (options.loadProviderConfig ?? loadProviderConfig)();

    if (!providerConfig) {
      return {
        check: check(
          'provider.config',
          'Provider config',
          'fail',
          'No LLM provider is configured.',
          {
            help: 'Run /provider or set OPENAI_API_KEY.',
          }
        ),
      };
    }

    return {
      value: providerConfig,
      check: check(
        'provider.config',
        'Provider config',
        'pass',
        `Provider config loaded from ${providerConfig.source}.`,
        {
          source: providerConfig.source,
          profileName: providerConfig.profileName,
          provider: providerConfig.provider ?? 'auto',
          baseURL: providerConfig.baseURL,
          model: providerConfig.model,
          apiKey: providerConfig.apiKey,
        }
      ),
    };
  } catch (err: unknown) {
    return {
      check: check(
        'provider.config',
        'Provider config',
        'fail',
        `Provider config could not be loaded: ${redactZeroErrorMessage(err)}`
      ),
    };
  }
}

function checkProviderRuntime(
  providerConfig: ProviderConfig | undefined
): { check: ZeroDoctorCheck; runtime?: ZeroResolvedProviderRuntime } {
  if (!providerConfig) {
    return {
      check: check(
        'provider.model',
        'Provider model',
        'warn',
        'Model validity was skipped because provider config is unavailable.'
      ),
    };
  }

  try {
    const runtime = resolveZeroProviderRuntime({
      provider: providerConfig.provider,
      apiKey: providerConfig.apiKey,
      baseURL: providerConfig.baseURL,
      model: providerConfig.model,
      profileName: providerConfig.profileName,
      source: providerConfig.source,
    });

    return {
      runtime,
      check: check(
        'provider.model',
        'Provider model',
        'pass',
        `Model ${runtime.modelId ?? runtime.requestedModel} resolves to ${runtime.provider}.`,
        {
          requestedModel: runtime.requestedModel,
          modelId: runtime.modelId,
          apiModel: runtime.apiModel,
          provider: runtime.provider,
          capabilities: runtime.capabilities,
        }
      ),
    };
  } catch (err: unknown) {
    return {
      check: check(
        'provider.model',
        'Provider model',
        'fail',
        `Provider model is invalid: ${redactZeroErrorMessage(err)}`
      ),
    };
  }
}

function checkProviderAdapter(
  runtime: ZeroResolvedProviderRuntime | undefined
): ZeroDoctorCheck {
  if (!runtime) {
    return check(
      'provider.adapter',
      'Provider adapter',
      'warn',
      'Provider adapter check was skipped because provider runtime did not resolve.'
    );
  }

  try {
    createZeroProvider(runtime);
    return check(
      'provider.adapter',
      'Provider adapter',
      'pass',
      `Provider adapter for ${runtime.provider} is available.`,
      {
        provider: runtime.provider,
        baseURL: runtime.baseURL,
        apiModel: runtime.apiModel,
        apiKey: runtime.apiKey,
      }
    );
  } catch (err: unknown) {
    return check(
      'provider.adapter',
      'Provider adapter',
      'fail',
      `Provider adapter is not usable: ${redactZeroErrorMessage(err)}`,
      {
        provider: runtime.provider,
        baseURL: runtime.baseURL,
        apiModel: runtime.apiModel,
      }
    );
  }
}

async function checkProviderConnectivity(
  runtime: ZeroResolvedProviderRuntime | undefined,
  options: ZeroDoctorOptions
): Promise<ZeroDoctorCheck> {
  if (!runtime) {
    return check(
      'provider.connectivity',
      'Provider connectivity',
      'warn',
      'Connectivity check was skipped because provider runtime did not resolve.'
    );
  }

  if (!options.connectivity) {
    return check(
      'provider.connectivity',
      'Provider connectivity',
      'warn',
      'Connectivity probe skipped. Run `zero doctor --connectivity` to probe the provider endpoint.',
      {
        baseURL: runtime.baseURL,
      }
    );
  }

  const fetchImpl = options.fetchImpl ?? fetch;
  const timeoutMs = options.connectivityTimeoutMs ?? DEFAULT_CONNECTIVITY_TIMEOUT_MS;
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);

  try {
    const response = await fetchImpl(runtime.baseURL, {
      method: 'HEAD',
      signal: controller.signal,
    });
    return check(
      'provider.connectivity',
      'Provider connectivity',
      'pass',
      `Provider endpoint responded with HTTP ${response.status}.`,
      {
        baseURL: runtime.baseURL,
        status: response.status,
      }
    );
  } catch (err: unknown) {
    return check(
      'provider.connectivity',
      'Provider connectivity',
      'fail',
      `Provider endpoint probe failed: ${redactZeroErrorMessage(err)}`,
      {
        baseURL: runtime.baseURL,
      }
    );
  } finally {
    clearTimeout(timer);
  }
}

function check(
  id: string,
  label: string,
  status: ZeroDoctorStatus,
  message: string,
  details?: Record<string, unknown>
): ZeroDoctorCheck {
  return redactZeroSecrets({
    id,
    label,
    status,
    message,
    ...(details ? { details } : {}),
  }) as ZeroDoctorCheck;
}

function validateConfigFile(label: 'user' | 'project', path: string): string | undefined {
  if (!existsSync(path)) return undefined;

  try {
    const parsed = JSON.parse(readFileSync(path, 'utf-8'));
    const result = ZeroConfigSchema.partial().safeParse(parsed);
    if (result.success) return undefined;
    return `${label} config ${path}: ${result.error.issues
      .map((issue) => issue.message)
      .join('; ')}`;
  } catch (err: unknown) {
    return `${label} config ${path}: ${redactZeroErrorMessage(err)}`;
  }
}

function formatDetails(details: Record<string, unknown> | undefined): string {
  if (!details) return '';

  const entries = Object.entries(details)
    .filter(([, value]) => value !== undefined)
    .map(([key, value]) => `${redactZeroString(key)}: ${formatDetailValue(value)}`);

  return entries.join(' | ');
}

function formatDetailValue(value: unknown): string {
  if (Array.isArray(value)) {
    return value.map(formatDetailValue).join(', ');
  }

  if (typeof value === 'object' && value !== null) {
    return redactZeroString(JSON.stringify(redactZeroSecrets(value)));
  }

  return redactZeroString(String(value));
}

function getBunVersion(): string | undefined {
  return typeof Bun !== 'undefined' ? Bun.version : undefined;
}

function defaultUserConfigPath(): string {
  return join(homedir(), '.config', 'zero', 'config.json');
}

function defaultProjectConfigPath(): string {
  return join(process.cwd(), '.zero', 'config.json');
}
