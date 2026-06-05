import { describe, expect, it } from 'bun:test';
import {
  DEFAULT_PERF_THRESHOLDS,
  evaluatePerfWarnings,
  formatPerfSummary,
  parsePerfBenchArgs,
  runPerfBench,
  summarizeSamples,
  type PerfBenchResult,
  type PerfMetrics,
} from '../scripts/perf-bench';

describe('performance benchmark helpers', () => {
  it('does not import the legacy TypeScript agent runtime', async () => {
    const source = await Bun.file('scripts/perf-bench.ts').text();

    expect(source).not.toContain('../src/agent/loop');
    expect(source).not.toContain('../src/providers/types');
  });

  it('summarizes samples with stable sorted output', () => {
    const stats = summarizeSamples([30.333, 10.111, 20.222, 40.444]);

    expect(stats.samples).toEqual([10.11, 20.22, 30.33, 40.44]);
    expect(stats.min).toBe(10.11);
    expect(stats.median).toBe(25.27);
    expect(stats.p95).toBe(40.44);
    expect(stats.max).toBe(40.44);
  });

  it('classifies threshold warnings without failing every metric', () => {
    const metrics: PerfMetrics = {
      coldStartMs: summarizeSamples([120, 301]),
      firstOutputMs: summarizeSamples([12, 18]),
      processDrainMs: summarizeSamples([0.2, 0.4]),
      harnessEndRssMb: summarizeSamples([80, 90]),
      harnessEndRssDeltaMb: summarizeSamples([1, 3]),
    };

    const warnings = evaluatePerfWarnings(metrics, DEFAULT_PERF_THRESHOLDS);

    expect(warnings).toHaveLength(1);
    expect(warnings[0]).toMatchObject({
      metric: 'coldStartMs',
      statistic: 'p95',
      observed: 301,
      threshold: 300,
      unit: 'ms',
    });
  });

  it('parses CLI and environment overrides', () => {
    const options = parsePerfBenchArgs(
      [
        '--iterations=3',
        '--warmup',
        '0',
        '--first-output-warn-ms=600',
        '--output=dist/perf/report.json',
        '--ci',
        '--fail-on-warning',
      ],
      {
        ZERO_PERF_COLD_START_WARN_MS: '250',
        ZERO_PERF_HARNESS_END_RSS_WARN_MB: '384',
      }
    );

    expect(options.iterations).toBe(3);
    expect(options.warmupIterations).toBe(0);
    expect(options.thresholds).toEqual({
      coldStartP95Ms: 250,
      firstOutputP95Ms: 600,
      harnessEndRssMaxMb: 384,
    });
    expect(options.output).toBe('dist/perf/report.json');
    expect(options.ci).toBe(true);
    expect(options.failOnWarning).toBe(true);

    const envOnly = parsePerfBenchArgs([], {
      ZERO_PERF_FIRST_OUTPUT_WARN_MS: '610',
    });
    expect(envOnly.thresholds.firstOutputP95Ms).toBe(610);
  });

  it('runs a minimal benchmark end to end', async () => {
    const result = await runPerfBench({
      iterations: 1,
      warmupIterations: 0,
      coldStartCommand: [process.execPath, '--version'],
      firstOutputCommand: [process.execPath, '--version'],
      thresholds: {
        coldStartP95Ms: 60_000,
        firstOutputP95Ms: 60_000,
        harnessEndRssMaxMb: 4096,
      },
    });

    expect(result.schemaVersion).toBe(2);
    expect(result.iterations).toBe(1);
    expect(result.metrics.coldStartMs.samples).toHaveLength(1);
    expect(result.metrics.firstOutputMs.samples).toHaveLength(1);
    expect(result.metrics.harnessEndRssMb.max).toBeGreaterThan(0);
    expect(result.warnings).toEqual([]);
  });

  it('formats the benchmark summary with warning details', () => {
    const metrics: PerfMetrics = {
      coldStartMs: summarizeSamples([100, 110]),
      firstOutputMs: summarizeSamples([20, 30]),
      processDrainMs: summarizeSamples([0.1, 0.2]),
      harnessEndRssMb: summarizeSamples([300, 310]),
      harnessEndRssDeltaMb: summarizeSamples([2, 4]),
    };
    const warnings = evaluatePerfWarnings(metrics, DEFAULT_PERF_THRESHOLDS);
    const result: PerfBenchResult = {
      schemaVersion: 2,
      timestamp: '2026-06-03T00:00:00.000Z',
      platform: {
        os: 'linux',
        arch: 'x64',
        bunVersion: '1.3.14',
      },
      coldStartCommand: ['/repo/zero', '--version'],
      firstOutputCommand: ['/repo/zero', '--version'],
      iterations: 2,
      warmupIterations: 1,
      thresholds: DEFAULT_PERF_THRESHOLDS,
      metrics,
      benchmarkDurationMs: 50,
      warnings,
    };

    const summary = formatPerfSummary(result);

    expect(summary).toContain('Zero performance benchmark');
    expect(summary).toContain('cold start: median 105.00 ms');
    expect(summary).toContain('harness end RSS: max 310.00 MB');
    expect(summary).toContain('warnings:');
    expect(summary).toContain('Benchmark harness end RSS 310.00 MB');
  });
});
