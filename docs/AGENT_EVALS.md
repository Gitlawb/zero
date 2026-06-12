# Offline Agent Evals

Zero agent evals are maintainer fixtures for checking coding-agent behavior
without calling a live model. They describe a task, the files the agent is
expected to change, the commands that should verify the result, and the scoring
rules an offline harness can apply to a captured run.

These fixtures are intentionally local-first. They do not prove provider quality
or live model execution by themselves; they give tests and CLI workflows a
stable sample suite to validate, run against copied workspaces, and score from
saved outputs. The eval harness is local and offline-testable. It only makes
live model calls when the supplied agent command does.

## Suite Format

Sample suites live under `internal/agenteval/testdata/`. Tiny fixture
workspaces live under `internal/agenteval/testdata/fixtures/`.

Each suite JSON file contains:

- `id`: stable suite identifier for filters and reports.
- `name` and `description`: maintainer-facing suite metadata.
- `tasks`: coding-agent tasks with prompts, file expectations, verification
  commands, and offline scoring inputs.

Task fields used by the sample suite:

- `id`: stable task identifier for filters and reports.
- `name` and `description`: short task metadata.
- `tags`: stable category labels such as `docs`, `go`, or `wrapper`.
- `difficulty`: a coarse task size such as `easy`, `medium`, or `hard`.
- `prompt`: the user request to give an agent.
- `workspaceFixture`: the fixture workspace to copy before running the task.
- `expectedChangedFiles`: files that should change for a complete solution.
- `forbiddenChangedFiles`: files that must not change during the task.
- `requiredTraceEvents`: JSONL agent events that must appear in benchmark
  stdout.
- `contextChecks`: required and forbidden files checked in the materialized
  workspace before verification commands run.
- `verificationCommands`: commands a maintainer or harness can run after the
  agent output is applied.

The scoring contract matches command results by `verificationCommands[].id`,
compares changed files against `expectedChangedFiles`, rejects any
`forbiddenChangedFiles`, checks the materialized workspace with `contextChecks`,
and can require agent trace events during benchmark runs. The loader rejects
unknown JSON fields so suite changes fail fast.

Example richer task rubric:

```json
{
  "tags": ["docs", "jsonl"],
  "difficulty": "easy",
  "forbiddenChangedFiles": ["go.mod", "package.json"],
  "requiredTraceEvents": ["tool:read_file", "verify:go-test"],
  "contextChecks": {
    "requiredFiles": ["docs/STREAM_JSON_PROTOCOL.md"],
    "forbiddenFiles": ["node_modules/cache.txt"]
  }
}
```

## Modes

### Validate Mode

`zero eval` defaults to `validate` mode. In validate mode, the command performs
schema and contract checks only: it parses the suite, rejects invalid task
definitions, and reports the number of tasks and checks. It does not copy
fixtures, invoke an agent, score a workspace, or execute verification commands.

```bash
go run ./cmd/zero eval --suite internal/agenteval/testdata/sample_suite.json
```

Use JSON output when another local tool needs the validation summary:

```bash
go run ./cmd/zero eval --suite internal/agenteval/testdata/sample_suite.json --json
```

### Run Mode

`zero eval run` scores one already-mutated Git workspace. It does not copy
fixtures or invoke an agent; point it at a Git worktree where a fixture has
already been copied, initialized, and attempted by an agent or deterministic
local script. The runner executes each `verificationCommands` entry, collects
changed files with `git status --porcelain`, and emits the task-success report
contract below. `--workspace` is required: it must point at the prepared fixture
worktree, never the current directory, so the suite's verification commands
(`go test`, `git`, ...) don't run against your real repo.

```bash
go run ./cmd/zero eval run \
  --suite internal/agenteval/testdata/sample_suite.json \
  --task document-stream-json-verify-events \
  --workspace /tmp/zero-eval-workspace
```

Persist the report for comparison between prompt or model changes:

```bash
go run ./cmd/zero eval run \
  --suite internal/agenteval/testdata/sample_suite.json \
  --task document-stream-json-verify-events \
  --workspace /tmp/zero-eval-workspace \
  --report-dir /tmp/zero-eval-report \
  --json
```

### Bench Mode

`zero eval bench` runs the full benchmark harness for one task or a suite. Bench
mode copies each task fixture into `--work-root`, initializes a clean Git
baseline, runs the supplied `--agent-command` in that workspace, then scores the
result with the same scorer used by run mode.

Agent commands are passed as argv, without shell interpolation. The harness
expands these placeholders in each argument:

- `{workspace}`: copied task workspace path.
- `{prompt}`: task prompt from the suite.
- `{task_id}`: selected task ID.
- `{model}`: current model ID for model-matrix benchmark runs.

Use `--model <id>` more than once, or `--models a,b,c`, to run the same task
matrix across several models. When no model is supplied, the harness preserves
the previous single-run behavior and `{model}` expands to an empty string.

Example using a real local agent command:

```bash
go run ./cmd/zero eval bench \
  --suite internal/agenteval/testdata/sample_suite.json \
  --task document-stream-json-verify-events \
  --work-root /tmp/zero-evals \
  --agent-command zero exec --cwd {workspace} {prompt}
```

Example with model selection:

```bash
go run ./cmd/zero eval bench \
  --suite internal/agenteval/testdata/sample_suite.json \
  --task document-stream-json-verify-events \
  --work-root /tmp/zero-evals \
  --model gpt-5 \
  --agent-command zero exec --model {model} --cwd {workspace} {prompt}
```

Include `{task_id}` when the agent wrapper needs stable per-task logging,
branching, or fixture-specific behavior:

```bash
go run ./cmd/zero eval bench \
  --suite internal/agenteval/testdata/sample_suite.json \
  --work-root /tmp/zero-evals \
  --agent-command zero-agent-wrapper --task {task_id} --workspace {workspace} --prompt {prompt}
```

The same wrapper can emit JSONL trace events to stdout for future trace scoring:

```json
{"type":"tool","name":"read_file"}
{"event":"verify","name":"go-test"}
```

Those events are required by adding keys such as `tool:read_file` and
`verify:go-test` to `requiredTraceEvents`.

For deterministic offline testing, point `--agent-command` at a local script
that edits the copied workspace without calling a model:

```bash
go run ./cmd/zero eval bench \
  --suite internal/agenteval/testdata/sample_suite.json \
  --task document-stream-json-verify-events \
  --work-root /tmp/zero-evals \
  --agent-command ./scripts/fake-agent --workspace {workspace} --task {task_id} --prompt {prompt}
```

Bound a benchmark run with `--timeout` (a Go duration) so a wedged or
interactive agent cannot block the harness forever. The timeout applies per
task and cancels materialization, the agent process, and scoring:

```bash
go run ./cmd/zero eval bench \
  --suite internal/agenteval/testdata/sample_suite.json \
  --task document-stream-json-verify-events \
  --work-root /tmp/zero-evals \
  --timeout 5m \
  --agent-command ./scripts/fake-agent --workspace {workspace} --prompt {prompt}
```

Use `--report-dir` to persist the CLI report artifact. The file is always named
`agent-eval-report.json`; with bench mode it records the suite status, task
counts, pass/fail totals, failures, and the nested benchmark report with each
task/model run. Use `--keep-workspaces` when you also need to inspect the
materialized workspaces after the run:

```bash
go run ./cmd/zero eval bench \
  --suite internal/agenteval/testdata/sample_suite.json \
  --task add-npm-wrapper-argv-helper \
  --work-root /tmp/zero-evals \
  --keep-workspaces \
  --report-dir /tmp/zero-eval-report \
  --json \
  --agent-command ./scripts/fake-agent --workspace {workspace} --task {task_id} --prompt {prompt}
```

**Scoring caveat:** changed-file scoring inspects the workspace with
`git status --porcelain` against the baseline commit. An agent that *commits*
its own changes (or otherwise leaves a clean working tree) defeats this check —
the committed edits no longer appear as changed files, so `expectedChangedFiles`
will not match. Agents under bench should leave their edits uncommitted.

Run the package tests when changing the suite schema or scorer:

```bash
go test ./internal/agenteval
```

For a faster manual fixture check:

```bash
go test ./internal/verify ./internal/selfverify
```

Or parse the JSON directly with any strict JSON parser. For example:

```bash
python -m json.tool internal/agenteval/testdata/sample_suite.json
```

The `internal/agenteval` tests load every JSON file under
`internal/agenteval/testdata/` and reject missing task IDs, empty verification
commands, and malformed changed-file expectations.

## Report JSON

Scored reports use contract `zero.agenteval.report.v1`.

- `suiteId` and `taskId`: identify the suite and selected task.
- `status`: overall `pass`, `fail`, `blocked`, or `error`.
- `ok`: true only when every result passes.
- `summary`: total result counts by status.
- `changedFiles`: normalized files collected from the workspace.
- `results`: one result per verification command, plus configured
  `changed_files`, `forbidden_changed_files`, `context_checks`, and
  `trace_events` checks.
- `error`: task-selection or report-level error, when present.

Command results include the command ID, display name, command argv, status,
exit code, stdout, stderr, and an optional message. File-based results include
expected, actual, missing, and unexpected files. Trace results include expected,
actual, and missing event keys.

## Score Interpretation

Scores are offline quality signals, not pass/fail release gates by default. The
statuses below are produced when a harness supplies captured command results and
changed files.

- `pass`: every verification command exited successfully, changed files matched
  `expectedChangedFiles`, forbidden files stayed untouched, configured context
  checks passed, and required trace events were present.
- `fail`: at least one command failed, changed files were missing or
  unexpected, a forbidden file changed, a context file check failed, or a
  required trace event was missing.
- `blocked`: the harness could not run the task or collect the expected inputs.
- `error`: the suite, task ID, command ID, or captured input could not be
  interpreted.

Real task-success measurement comes from the combination of prompt, fixture,
verification commands, and changed-file expectations. Prefer comparing results
between runs of the same suite revision. Do not compare results across suites
unless the task mix and scoring contract are unchanged.
