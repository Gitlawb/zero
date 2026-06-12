# Zero Task Benchmark

This is the **task** benchmark: how often ZERO completes a real coding task
end-to-end, headless, unattended. It is separate from
[`docs/PERFORMANCE.md`](PERFORMANCE.md), which measures process startup and
memory, not task success.

The methodology is the point, not the digit. A task-success score is **largely
model-bounded** — most of the number comes from whichever model you bring. So we
record the model with every result and we publish the score **with and without
the self-correct loop**, because the delta between those two runs is the part
ZERO actually contributes: the agent noticing its own broken edit and fixing it
before it hands the task back.

## What is recorded

Every run produces a self-describing JSON record so a published number is
reproducible and auditable from the record alone:

| Field            | Meaning                                                       |
| ---------------- | ------------------------------------------------------------- |
| `suite`          | Task set id (which tasks produced the number)                 |
| `model`          | The model that ran — the score is model-bounded, so this is required |
| `mode`           | Exec mode preset, if any                                      |
| `selfCorrect`    | Whether the post-edit verify-and-correct loop was enabled     |
| `version`        | ZERO version                                                  |
| `commit`         | ZERO commit the run was built from                            |
| `date`           | UTC timestamp of the run                                      |
| `tasksAttempted` | Tasks attempted                                               |
| `tasksPassed`    | Tasks whose verification passed                               |
| `passRate`       | `tasksPassed / tasksAttempted`                                |
| `tasks`          | Per-task pass/fail/error with detail                          |

## Integration point: headless `zero exec`

The harness drives ZERO through its headless surface — the same path CI uses:

```bash
zero exec --output-format stream-json --model <model> [--self-correct] "<task prompt>"
```

Per task, the harness reads the terminal `run_end` event's exit code from the
stream-json output to decide pass/fail. When a task carries a
`verificationCommand` (e.g. `go test ./...`), that command's exit status is
authoritative — mirroring Terminal-Bench's external-verifier model: the task is
"passed" only when the project's own checks pass after the agent finishes.

## Task set format

A task set is a JSON manifest. A runnable sample lives at
[`cmd/zero-perf-bench/testdata/terminal-bench-sample.json`](../cmd/zero-perf-bench/testdata/terminal-bench-sample.json):

```json
{
  "id": "terminal-bench-sample",
  "name": "Terminal-Bench (sample)",
  "tasks": [
    {
      "id": "hello-fix",
      "name": "make the failing test pass",
      "prompt": "The test in ./hello fails. Fix the implementation so `go test ./...` passes.",
      "workspaceFixture": "./hello",
      "verificationCommand": ["go", "test", "./..."]
    }
  ]
}
```

## The exact command

Build the binary, then run the task harness **twice** against the same task set
and the same model — once without self-correct, once with — and stamp the version
and commit so the records are reproducible:

```bash
# build the production binary
go run ./cmd/zero-release build

VERSION=$(git describe --tags --always)
COMMIT=$(git rev-parse --short HEAD)
SUITE=cmd/zero-perf-bench/testdata/terminal-bench-sample.json
MODEL=<your-model>

# baseline: self-correct OFF
go run ./cmd/zero-perf-bench tasks \
  --suite "$SUITE" --binary ./zero --model "$MODEL" \
  --version "$VERSION" --commit "$COMMIT" \
  --output dist/bench/tasks-baseline.json

# self-correct ON (auto-fix needs --auto medium or high; see note below)
go run ./cmd/zero-perf-bench tasks \
  --suite "$SUITE" --binary ./zero --model "$MODEL" --self-correct \
  --version "$VERSION" --commit "$COMMIT" \
  --output dist/bench/tasks-selfcorrect.json
```

`--version`/`--commit` also read from `ZERO_BENCH_VERSION` / `ZERO_BENCH_COMMIT`
when the flags are omitted, so CI can stamp them once in the environment.

Use `--dry-run` to exercise the record path without invoking a model (every task
is recorded as skipped) — useful for validating a task set before a real run.

> **Self-correct and autonomy.** `--self-correct` runs the verify-and-correct
> loop after each mutating edit. Whether a detected failure is **auto-fixed** or
> only **reported** is gated by the run's autonomy: pass `--auto medium` (or
> `high`) for the loop to drive corrective rounds. At the default low autonomy it
> reports failures without auto-fixing, so the with/without delta is measured at
> `--auto medium` or higher.

## Published result

Fill in after a clean run on a fixed machine. Keep both records; the delta is the
headline.

| Run                     | Model     | Self-correct | Pass rate | Commit |
| ----------------------- | --------- | ------------ | --------- | ------ |
| Baseline                | _TBD_     | off          | _TBD_     | _TBD_  |
| With self-correct       | _TBD_     | on           | _TBD_     | _TBD_  |
| **Self-correct delta**  |           |              | **_TBD_** |        |

Report the model alongside the number every time. A score without its model is
not a claim about ZERO — it is a claim about the model. The honest signal is the
delta: how much the self-correct loop moved the same model on the same tasks.

## Reproducing a published number

1. Check out the `commit` from the record.
2. `go run ./cmd/zero-release build`.
3. Run the two commands above with the recorded `model` and `suite`.
4. Compare `passRate` in the new records against the published ones.
