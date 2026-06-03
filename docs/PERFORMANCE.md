# Zero Performance Benchmarks

The M2 performance harness tracks three release-facing signals:

- Cold start: process startup time for `zero --version`.
- TTFT: time from starting the local agent loop to the first streamed text event from a deterministic provider.
- Memory: peak RSS for the benchmarked in-process agent path.

When a built binary exists at `./zero` or `./zero.exe`, cold start uses that artifact. Otherwise it falls back to `bun src/index.ts --version` so the benchmark can run before a local build.

## Run Locally

```bash
bun run perf:bench
```

Run against a freshly built binary:

```bash
bun run build
bun run perf:bench
```

Write the JSON report used by CI:

```bash
bun run perf:smoke
```

Default warning thresholds:

- Cold start p95: 300 ms
- TTFT p95: 500 ms
- Agent RSS peak: 256 MB

The default sample count is intentionally small for CI smoke coverage. `p95` uses nearest-rank percentile selection, so with the default 5 measured samples it is the slowest sample. Increase `--iterations` for local baseline investigations.

Override thresholds with CLI flags:

```bash
bun run scripts/perf-bench.ts --cold-start-warn-ms=350 --ttft-warn-ms=600 --rss-warn-mb=384
```

Or with environment variables:

```bash
ZERO_PERF_COLD_START_WARN_MS=350 bun run perf:bench
```

Supported environment variables:

- `ZERO_PERF_ITERATIONS`
- `ZERO_PERF_WARMUP_ITERATIONS`
- `ZERO_PERF_COLD_START_WARN_MS`
- `ZERO_PERF_TTFT_WARN_MS`
- `ZERO_PERF_RSS_WARN_MB`

## CI Behavior

The `Performance Smoke` job builds the binary, runs `bun run perf:smoke`, and uploads `dist/perf/perf-bench.json`.

Threshold drift is emitted as GitHub Actions warnings. The job fails only if the benchmark cannot run, the build fails, or `--fail-on-warning` is passed explicitly.
