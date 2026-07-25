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
ranked by **exclusive** time, per-class roll-ups, and token/count totals.
That top-three list is the Phase 0 baseline's "do not proceed until" criterion —
it names where a turn actually spends time so later optimization work is
targeted, not guessed.

### Attribution model (honest by construction)

Spans record wall intervals and are **not** summed into each other. Each span's
**exclusive** time is its own duration minus the union of its nested children's
intervals, derived at finish by interval containment. So a `provider_connect`
that runs concurrently inside `generation`, or a `permission_wait` nested inside
`tool_execution`, each contributes only its own exclusive time — they no longer
double-count the same wall. The top-latency shares therefore sum to ~1 for a
well-instrumented run, and the ranking reflects where wall time is actually
spent.

**Coverage** is the fraction of wall covered by the union of all span
intervals (capped at 1.0) — the honest "≥95% of wall accounted for" metric. A
run with `coverage < 0.95` has uninstrumented gaps, not an inflated attribution.

### Pass/fail is reported per oracle tier

Pass/fail is split into three tiers so it cannot be misread as a blanket
correctness verdict (see `MANIFEST.md` for the class breakdown):

- **Correctness** (`tasksVerified` / `tasksPassed` / `correctnessPassRate`):
  tasks with a positive oracle — edit's substring grep, fix's scoped `go test`.
  This is the only pass rate that can move with model quality.
- **Build-only** (`buildCheckedTasks` / `buildPassedTasks` / `buildPassRate`):
  refactor's `go build ./...`, which proves the edit compiles but not that the
  refactor achieved its goal. Reported separately, never in `correctnessPassRate`.
- **Latency-only** (`latencyOnlyTasks`): the read-only classes (nav, longproc,
  longctx, parallel) carry no oracle — an exit 0 only proves the turn ran. They
  contribute to latency and span attribution and are excluded from every pass
  rate.

`tasksAttempted` is still the total across all three tiers. The tier class lists
are echoed in the report so a consumer can see exactly which classes each rate
is computed over.

> **Do not average the tier pass rates. Do not report a single "pass rate".**
> `correctnessPassRate` and `buildPassRate` measure different things over
> different task sets and must never be combined — a weighted or arithmetic
> mean of the two is a number that means nothing. The schema offers no
> headline pass rate on purpose: a consumer must name the tier it is reporting
> (`correctness`, `build`, or `latency-only`) and quote that tier's fields. A
> read-only run that exits 0 is a latency sample, not a pass.

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