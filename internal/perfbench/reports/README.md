# Per-turn benchmark reports

This directory holds generated per-turn benchmark reports. It is **not** a
checked-in source of truth for performance — the numbers are machine- and
model-specific, so a single snapshot here would mislead rather than inform.

## Generating a baseline

Run the harness over the checked-in manifest:

```sh
make baseline ZERO_BENCH_MODEL=<model>              # uses ./zero
make baseline ZERO_BENCH_MODEL=<model> ZERO_BENCH_BINARY=/path/to/zero
```

This builds `zero`, then runs `zero-perf-bench turn` over
`internal/perfbench/manifests/baseline.json`, capturing each turn's trace
(`zero exec --trace <tmpfile>`) and writing the aggregated result to
`reports/baseline.json`.

The JSON report is self-describing: model, mode, self-correct flag, version,
commit, date, per-span median/P95, the **top three controllable latency sources**
ranked by share of attributed time, per-class roll-ups, and token/count totals.
That top-three list is the Phase 0 baseline's "do not proceed until" criterion —
it names where a turn actually spends time so later optimization work is
targeted, not guessed.

## What to commit

Commit the manifest and fixtures (`manifests/`, `../testdata/`), not a generated
`baseline.json`. A generated report belongs in a PR description or a shared
dashboard as evidence for one configuration, not in the tree as a durable
expectation. `.gitkeep` keeps the directory present between runs.

## Caveats

- Each task is a fresh `zero exec` process, so iterations are **cold-start**
  samples. A warm path (reusing an in-process agent) is future work.
- Mutating tasks (edit/fix/refactor) run against a per-invocation **copy** of
  their fixture, so the checked-in fixtures stay clean and one task's edits
  can't bleed into the next iteration.