import { mkdir, writeFile } from 'node:fs/promises';
import { dirname } from 'node:path';
import { zeroArtifactName, zeroArtifactPath } from './artifact';

export interface PerfThresholds {
  coldStartP95Ms: number;
  firstOutputP95Ms: number;
  harnessEndRssMaxMb: number;
}

export interface NumericStats {
  samples: number[];
  min: number;
  median: number;
  average: number;
  p95: number;
  max: number;
}

export interface PerfMetrics {
  coldStartMs: NumericStats;
  firstOutputMs: NumericStats;
  processDrainMs: NumericStats;
  harnessEndRssMb: NumericStats;
  harnessEndRssDeltaMb: NumericStats;
}

export interface PerfWarning {
  metric: keyof PerfMetrics;
  statistic: keyof NumericStats;
  observed: number;
  threshold: number;
  unit: 'ms' | 'MB';
  message: string;
}

export interface PerfBenchResult {
  schemaVersion: 2;
  timestamp: string;
  platform: {
    os: NodeJS.Platform;
    arch: string;
    bunVersion: string;
  };
  coldStartCommand: string[];
  firstOutputCommand: string[];
  iterations: number;
  warmupIterations: number;
  thresholds: PerfThresholds;
  metrics: PerfMetrics;
  benchmarkDurationMs: number;
  warnings: PerfWarning[];
}

export interface PerfBenchOptions {
  iterations: number;
  warmupIterations: number;
  thresholds: PerfThresholds;
  coldStartCommand?: string[];
  firstOutputCommand?: string[];
}

export interface PerfBenchCliOptions extends PerfBenchOptions {
  ci: boolean;
  json: boolean;
  output?: string;
  failOnWarning: boolean;
  help: boolean;
}

interface FirstOutputSample {
  firstOutputMs: number;
  processDrainMs: number;
  harnessEndRssMb: number;
  harnessEndRssDeltaMb: number;
}

const DEFAULT_ITERATIONS = 5;
const DEFAULT_WARMUP_ITERATIONS = 1;

export const DEFAULT_PERF_THRESHOLDS: PerfThresholds = {
  coldStartP95Ms: 300,
  firstOutputP95Ms: 500,
  harnessEndRssMaxMb: 256,
};

export function summarizeSamples(samples: number[]): NumericStats {
  if (samples.length === 0) {
    throw new Error('Cannot summarize an empty sample set');
  }

  const sorted = samples.map(roundMetric).sort((a, b) => a - b);
  const total = sorted.reduce((sum, value) => sum + value, 0);

  return {
    samples: sorted,
    min: sorted[0]!,
    median: median(sorted),
    average: roundMetric(total / sorted.length),
    p95: percentile(sorted, 95),
    max: sorted[sorted.length - 1]!,
  };
}

export function evaluatePerfWarnings(
  metrics: PerfMetrics,
  thresholds: PerfThresholds
): PerfWarning[] {
  return [
    createWarning(
      'coldStartMs',
      'p95',
      metrics.coldStartMs.p95,
      thresholds.coldStartP95Ms,
      'ms',
      'Cold start p95'
    ),
    createWarning(
      'firstOutputMs',
      'p95',
      metrics.firstOutputMs.p95,
      thresholds.firstOutputP95Ms,
      'ms',
      'Binary first-output p95'
    ),
    createWarning(
      'harnessEndRssMb',
      'max',
      metrics.harnessEndRssMb.max,
      thresholds.harnessEndRssMaxMb,
      'MB',
      'Benchmark harness end RSS'
    ),
  ].filter((warning): warning is PerfWarning => warning !== undefined);
}

export function formatPerfSummary(result: PerfBenchResult): string {
  const lines = [
    `Zero performance benchmark (${result.platform.os}/${result.platform.arch}, Bun ${result.platform.bunVersion})`,
    `command: ${formatCommand(result.coldStartCommand)}`,
    `first-output command: ${formatCommand(result.firstOutputCommand)}`,
    `iterations: ${result.iterations} measured, ${result.warmupIterations} warmup`,
    `cold start: median ${formatMetric(result.metrics.coldStartMs.median, 'ms')}, p95 ${formatMetric(result.metrics.coldStartMs.p95, 'ms')} (warn > ${formatMetric(result.thresholds.coldStartP95Ms, 'ms')})`,
    `binary first output: median ${formatMetric(result.metrics.firstOutputMs.median, 'ms')}, p95 ${formatMetric(result.metrics.firstOutputMs.p95, 'ms')} (warn > ${formatMetric(result.thresholds.firstOutputP95Ms, 'ms')})`,
    `process drain: median ${formatMetric(result.metrics.processDrainMs.median, 'ms')}, p95 ${formatMetric(result.metrics.processDrainMs.p95, 'ms')}`,
    `harness end RSS: max ${formatMetric(result.metrics.harnessEndRssMb.max, 'MB')}, max end delta ${formatMetric(result.metrics.harnessEndRssDeltaMb.max, 'MB')} (warn > ${formatMetric(result.thresholds.harnessEndRssMaxMb, 'MB')})`,
  ];

  if (result.warnings.length > 0) {
    lines.push('warnings:');
    for (const warning of result.warnings) {
      lines.push(`- ${warning.message}`);
    }
  } else {
    lines.push('warnings: none');
  }

  return lines.join('\n');
}

export function parsePerfBenchArgs(
  args: string[],
  env: NodeJS.ProcessEnv = process.env
): PerfBenchCliOptions {
  const options: PerfBenchCliOptions = {
    iterations: readPositiveIntegerEnv(env, 'ZERO_PERF_ITERATIONS', DEFAULT_ITERATIONS),
    warmupIterations: readNonNegativeIntegerEnv(
      env,
      'ZERO_PERF_WARMUP_ITERATIONS',
      DEFAULT_WARMUP_ITERATIONS
    ),
    thresholds: {
      coldStartP95Ms: readPositiveNumberEnv(
        env,
        'ZERO_PERF_COLD_START_WARN_MS',
        DEFAULT_PERF_THRESHOLDS.coldStartP95Ms
      ),
      firstOutputP95Ms: readPositiveNumberEnv(
        env,
        'ZERO_PERF_FIRST_OUTPUT_WARN_MS',
        DEFAULT_PERF_THRESHOLDS.firstOutputP95Ms
      ),
      harnessEndRssMaxMb: readPositiveNumberEnv(
        env,
        'ZERO_PERF_HARNESS_END_RSS_WARN_MB',
        DEFAULT_PERF_THRESHOLDS.harnessEndRssMaxMb
      ),
    },
    ci: env.GITHUB_ACTIONS === 'true',
    json: false,
    failOnWarning: false,
    help: false,
  };

  for (let index = 0; index < args.length; index++) {
    const rawArg = args[index]!;
    const { flag, inlineValue } = splitFlagValue(rawArg);

    switch (flag) {
      case '--iterations':
        options.iterations = parsePositiveInteger(flag, readOptionValue(args, inlineValue, ++index, flag));
        if (inlineValue !== undefined) index--;
        break;
      case '--warmup':
        options.warmupIterations = parseNonNegativeInteger(
          flag,
          readOptionValue(args, inlineValue, ++index, flag)
        );
        if (inlineValue !== undefined) index--;
        break;
      case '--cold-start-warn-ms':
        options.thresholds.coldStartP95Ms = parsePositiveNumber(
          flag,
          readOptionValue(args, inlineValue, ++index, flag)
        );
        if (inlineValue !== undefined) index--;
        break;
      case '--first-output-warn-ms':
        options.thresholds.firstOutputP95Ms = parsePositiveNumber(
          flag,
          readOptionValue(args, inlineValue, ++index, flag)
        );
        if (inlineValue !== undefined) index--;
        break;
      case '--harness-end-rss-warn-mb':
        options.thresholds.harnessEndRssMaxMb = parsePositiveNumber(
          flag,
          readOptionValue(args, inlineValue, ++index, flag)
        );
        if (inlineValue !== undefined) index--;
        break;
      case '--output':
        options.output = readOptionValue(args, inlineValue, ++index, flag);
        if (inlineValue !== undefined) index--;
        break;
      case '--ci':
        rejectInlineValue(flag, inlineValue);
        options.ci = true;
        break;
      case '--json':
        rejectInlineValue(flag, inlineValue);
        options.json = true;
        break;
      case '--fail-on-warning':
        rejectInlineValue(flag, inlineValue);
        options.failOnWarning = true;
        break;
      case '--help':
      case '-h':
        rejectInlineValue(flag, inlineValue);
        options.help = true;
        break;
      default:
        throw new Error(`Unknown option: ${rawArg}`);
    }
  }

  return options;
}

export function perfBenchHelp(): string {
  return [
    'Usage: bun run scripts/perf-bench.ts [options]',
    '',
    'Options:',
    '  --iterations <n>             Measured samples to collect (default: 5)',
    '  --warmup <n>                 Warmup samples to discard (default: 1)',
    '  --cold-start-warn-ms <n>     Warn when cold-start p95 is above n ms',
    '  --first-output-warn-ms <n>   Warn when binary first-output p95 is above n ms',
    '  --harness-end-rss-warn-mb <n>',
    '                               Warn when harness end-state RSS is above n MB',
    '  --output <path>              Write the JSON report to path',
    '  --json                       Print only the JSON report',
    '  --ci                         Emit GitHub Actions warning annotations',
    '  --fail-on-warning            Exit non-zero when thresholds are exceeded',
    '  -h, --help                   Show this help',
    '',
    'Environment overrides:',
    '  ZERO_PERF_ITERATIONS, ZERO_PERF_WARMUP_ITERATIONS',
    '  ZERO_PERF_COLD_START_WARN_MS, ZERO_PERF_FIRST_OUTPUT_WARN_MS',
    '  ZERO_PERF_HARNESS_END_RSS_WARN_MB',
  ].join('\n');
}

export async function runPerfBench(options: PerfBenchOptions): Promise<PerfBenchResult> {
  const benchmarkStartedAt = performance.now();
  const coldStartCommand = options.coldStartCommand ?? await resolveColdStartCommand();
  const firstOutputCommand = options.firstOutputCommand ?? await resolveFirstOutputCommand();
  const coldStartSamples: number[] = [];
  const firstOutputSamples: FirstOutputSample[] = [];

  for (let i = 0; i < options.warmupIterations; i++) {
    await measureColdStart(coldStartCommand);
    await measureFirstOutput(firstOutputCommand);
  }

  for (let i = 0; i < options.iterations; i++) {
    coldStartSamples.push(await measureColdStart(coldStartCommand));
    firstOutputSamples.push(await measureFirstOutput(firstOutputCommand));
  }

  const metrics: PerfMetrics = {
    coldStartMs: summarizeSamples(coldStartSamples),
    firstOutputMs: summarizeSamples(firstOutputSamples.map((sample) => sample.firstOutputMs)),
    processDrainMs: summarizeSamples(firstOutputSamples.map((sample) => sample.processDrainMs)),
    harnessEndRssMb: summarizeSamples(
      firstOutputSamples.map((sample) => sample.harnessEndRssMb)
    ),
    harnessEndRssDeltaMb: summarizeSamples(
      firstOutputSamples.map((sample) => sample.harnessEndRssDeltaMb)
    ),
  };

  const result: PerfBenchResult = {
    schemaVersion: 2,
    timestamp: new Date().toISOString(),
    platform: {
      os: process.platform,
      arch: process.arch,
      bunVersion: Bun.version,
    },
    coldStartCommand,
    firstOutputCommand,
    iterations: options.iterations,
    warmupIterations: options.warmupIterations,
    thresholds: options.thresholds,
    metrics,
    benchmarkDurationMs: roundMetric(performance.now() - benchmarkStartedAt),
    warnings: [],
  };

  result.warnings = evaluatePerfWarnings(metrics, options.thresholds);
  return result;
}

async function main(): Promise<void> {
  try {
    const options = parsePerfBenchArgs(process.argv.slice(2));

    if (options.help) {
      console.log(perfBenchHelp());
      return;
    }

    const result = await runPerfBench(options);

    if (options.output) {
      await mkdir(dirname(options.output), { recursive: true });
      await writeFile(options.output, `${JSON.stringify(result, null, 2)}\n`, 'utf-8');
    }

    if (options.json) {
      console.log(JSON.stringify(result, null, 2));
    } else {
      console.log(formatPerfSummary(result));
    }

    if (options.ci) {
      emitPerfWarnings(result);
    }

    if (options.failOnWarning && result.warnings.length > 0) {
      process.exitCode = 1;
    }
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    console.error(`[zero] Performance benchmark failed: ${message}`);
    process.exitCode = 1;
  }
}

async function resolveColdStartCommand(): Promise<string[]> {
  if (await Bun.file(zeroArtifactPath).exists()) {
    return [zeroArtifactPath, '--version'];
  }

  throw new Error(`No ${zeroArtifactName} binary found. Run \`bun run build\` before running the performance benchmark.`);
}

async function resolveFirstOutputCommand(): Promise<string[]> {
  if (await Bun.file(zeroArtifactPath).exists()) {
    return [zeroArtifactPath, '--version'];
  }

  throw new Error(`No ${zeroArtifactName} binary found. Run \`bun run build\` before running the performance benchmark.`);
}

async function measureColdStart(command: string[]): Promise<number> {
  const startedAt = performance.now();
  const child = Bun.spawn(command, {
    stdout: 'pipe',
    stderr: 'pipe',
    env: {
      ...process.env,
      NO_COLOR: '1',
    },
  });

  const [exitCode, stdout, stderr] = await Promise.all([
    child.exited,
    readSpawnStream(child.stdout),
    readSpawnStream(child.stderr),
  ]);
  const durationMs = roundMetric(performance.now() - startedAt);

  if (exitCode !== 0) {
    throw new Error(
      `${formatCommand(command)} exited with ${exitCode}: ${stderr.trim() || stdout.trim() || 'no output'}`
    );
  }

  return durationMs;
}

async function measureFirstOutput(command: string[]): Promise<FirstOutputSample> {
  const rssBefore = readRssMb();
  const startedAt = performance.now();
  const child = Bun.spawn(command, {
    stdout: 'pipe',
    stderr: 'pipe',
    env: offlineBenchmarkEnv(),
  });

  let firstOutputAt: number | undefined;
  const [exitCode, stdout, stderr] = await Promise.all([
    child.exited,
    readTimedSpawnStream(child.stdout, () => {
      firstOutputAt ??= performance.now();
    }),
    readTimedSpawnStream(child.stderr, () => {
      firstOutputAt ??= performance.now();
    }),
  ]);
  const finishedAt = performance.now();
  const rssAfter = readRssMb();
  const observedFirstOutputAt = firstOutputAt ?? finishedAt;

  if (exitCode !== 0) {
    throw new Error(
      `${formatCommand(command)} exited with ${exitCode}: ${stderr.trim() || stdout.trim() || 'no output'}`
    );
  }

  return {
    firstOutputMs: roundMetric(observedFirstOutputAt - startedAt),
    processDrainMs: roundMetric(Math.max(0, finishedAt - observedFirstOutputAt)),
    harnessEndRssMb: roundMetric(rssAfter),
    harnessEndRssDeltaMb: roundMetric(Math.max(0, rssAfter - rssBefore)),
  };
}

function createWarning(
  metric: keyof PerfMetrics,
  statistic: keyof NumericStats,
  observed: number,
  threshold: number,
  unit: 'ms' | 'MB',
  label: string
): PerfWarning | undefined {
  if (observed <= threshold) return undefined;

  return {
    metric,
    statistic,
    observed,
    threshold,
    unit,
    message: `${label} ${formatMetric(observed, unit)} exceeded warning threshold ${formatMetric(threshold, unit)}`,
  };
}

function emitPerfWarnings(result: PerfBenchResult): void {
  for (const warning of result.warnings) {
    console.warn(`::warning title=Zero performance::${escapeActionCommand(warning.message)}`);
  }
}

function readRssMb(): number {
  return process.memoryUsage().rss / 1024 / 1024;
}

async function readSpawnStream(stream: ReadableStream<Uint8Array> | null): Promise<string> {
  if (!stream) return '';
  return new Response(stream).text();
}

async function readTimedSpawnStream(
  stream: ReadableStream<Uint8Array> | null,
  onFirstChunk: () => void
): Promise<string> {
  if (!stream) return '';

  const reader = stream.getReader();
  const chunks: Uint8Array[] = [];

  while (true) {
    const { value, done } = await reader.read();
    if (done) break;
    if (value.length > 0 && chunks.length === 0) {
      onFirstChunk();
    }
    if (value.length > 0) {
      chunks.push(value);
    }
  }

  return new TextDecoder().decode(joinChunks(chunks));
}

function joinChunks(chunks: Uint8Array[]): Uint8Array {
  const length = chunks.reduce((total, chunk) => total + chunk.length, 0);
  const joined = new Uint8Array(length);
  let offset = 0;

  for (const chunk of chunks) {
    joined.set(chunk, offset);
    offset += chunk.length;
  }

  return joined;
}

function offlineBenchmarkEnv(): NodeJS.ProcessEnv {
  const env = { ...process.env };
  delete env.ZERO_PROVIDER_COMMAND;
  delete env.ZERO_PROVIDER;
  delete env.OPENAI_API_KEY;
  delete env.OPENAI_BASE_URL;
  delete env.OPENAI_MODEL;
  delete env.ANTHROPIC_API_KEY;
  delete env.ANTHROPIC_BASE_URL;
  delete env.ANTHROPIC_MODEL;
  delete env.GEMINI_API_KEY;
  delete env.GEMINI_BASE_URL;
  delete env.GEMINI_MODEL;
  delete env.GOOGLE_API_KEY;
  delete env.GOOGLE_BASE_URL;
  delete env.GOOGLE_MODEL;
  env.NO_COLOR = '1';
  return env;
}

function percentile(sortedSamples: number[], percentileValue: number): number {
  const index = Math.max(
    0,
    Math.min(sortedSamples.length - 1, Math.ceil((percentileValue / 100) * sortedSamples.length) - 1)
  );
  return sortedSamples[index]!;
}

function median(sortedSamples: number[]): number {
  const middle = Math.floor(sortedSamples.length / 2);

  if (sortedSamples.length % 2 === 1) {
    return sortedSamples[middle]!;
  }

  return roundMetric((sortedSamples[middle - 1]! + sortedSamples[middle]!) / 2);
}

function roundMetric(value: number): number {
  return Number(value.toFixed(2));
}

function formatMetric(value: number, unit: 'ms' | 'MB'): string {
  return `${roundMetric(value).toFixed(2)} ${unit}`;
}

function formatCommand(command: string[]): string {
  return command.map((part) => (/\s/.test(part) ? JSON.stringify(part) : part)).join(' ');
}

function splitFlagValue(arg: string): { flag: string; inlineValue?: string } {
  const separatorIndex = arg.indexOf('=');
  if (separatorIndex === -1) return { flag: arg };

  return {
    flag: arg.slice(0, separatorIndex),
    inlineValue: arg.slice(separatorIndex + 1),
  };
}

function readOptionValue(
  args: string[],
  inlineValue: string | undefined,
  index: number,
  flag: string
): string {
  if (inlineValue !== undefined) {
    if (inlineValue === '') {
      throw new Error(`${flag} requires a value`);
    }
    return inlineValue;
  }

  return requireValue(args, index, flag);
}

function requireValue(args: string[], index: number, flag: string): string {
  const value = args[index];
  if (!value || value.startsWith('--')) {
    throw new Error(`${flag} requires a value`);
  }
  return value;
}

function rejectInlineValue(flag: string, inlineValue: string | undefined): void {
  if (inlineValue !== undefined) {
    throw new Error(`${flag} does not accept a value`);
  }
}

function readPositiveIntegerEnv(
  env: NodeJS.ProcessEnv,
  name: string,
  fallback: number
): number {
  const value = env[name];
  return value === undefined ? fallback : parsePositiveInteger(name, value);
}

function readNonNegativeIntegerEnv(
  env: NodeJS.ProcessEnv,
  name: string,
  fallback: number
): number {
  const value = env[name];
  return value === undefined ? fallback : parseNonNegativeInteger(name, value);
}

function readPositiveNumberEnv(env: NodeJS.ProcessEnv, name: string, fallback: number): number {
  const value = env[name];
  return value === undefined ? fallback : parsePositiveNumber(name, value);
}

function parsePositiveInteger(name: string, value: string): number {
  const parsed = Number(value);
  if (!Number.isInteger(parsed) || parsed < 1) {
    throw new Error(`${name} must be a positive integer`);
  }
  return parsed;
}

function parseNonNegativeInteger(name: string, value: string): number {
  const parsed = Number(value);
  if (!Number.isInteger(parsed) || parsed < 0) {
    throw new Error(`${name} must be a non-negative integer`);
  }
  return parsed;
}

function parsePositiveNumber(name: string, value: string): number {
  const parsed = Number(value);
  if (!Number.isFinite(parsed) || parsed <= 0) {
    throw new Error(`${name} must be a positive number`);
  }
  return parsed;
}

function escapeActionCommand(value: string): string {
  return value.replaceAll('%', '%25').replaceAll('\r', '%0D').replaceAll('\n', '%0A');
}

if (import.meta.main) {
  await main();
}
