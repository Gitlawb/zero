# Zero Performance Benchmarks

The M2 performance harness tracks three release-facing signals:

- Cold start: process startup time for `zero --version`.
- Binary first output: time from spawning the built `zero --version` command to
  the first stdout or stderr chunk.
- Harness end memory: RSS for the Bun benchmark harness after the spawned
  command exits, plus the delta from the pre-spawn RSS sample.

Cold start uses the built Go binary at `./zero` or `./zero.exe`. Run `bun run build` before the benchmark so it measures the production runtime rather than the old TypeScript entrypoint.

This smoke benchmark does not measure provider TTFT or Go agent memory. A
provider-aware Go benchmark should be added separately when the runtime exposes a
deterministic local streaming path.

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
- Binary first-output p95: 500 ms
- Harness end RSS max: 256 MB

The default sample count is intentionally small for CI smoke coverage. `p95` uses nearest-rank percentile selection, so with the default 5 measured samples it is the slowest sample. Increase `--iterations` for local baseline investigations.

Override thresholds with CLI flags:

```bash
bun run scripts/perf-bench.ts --cold-start-warn-ms=350 --first-output-warn-ms=600 --harness-end-rss-warn-mb=384
```

Or with environment variables:

```bash
ZERO_PERF_COLD_START_WARN_MS=350 bun run perf:bench
```

Supported environment variables:

- `ZERO_PERF_ITERATIONS`
- `ZERO_PERF_WARMUP_ITERATIONS`
- `ZERO_PERF_COLD_START_WARN_MS`
- `ZERO_PERF_FIRST_OUTPUT_WARN_MS`
- `ZERO_PERF_HARNESS_END_RSS_WARN_MB`

## CI Behavior

The `Performance Smoke` job builds the binary, runs `bun run perf:smoke`, and uploads `dist/perf/perf-bench.json`.

Threshold drift is emitted as GitHub Actions warnings. The job fails only if the benchmark cannot run, the build fails, or `--fail-on-warning` is passed explicitly.
