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
- `prompt`: the user request to give an agent.
- `workspaceFixture`: the fixture workspace to copy before running the task.
- `expectedChangedFiles`: files that should change for a complete solution.
- `verificationCommands`: commands a maintainer or harness can run after the
  agent output is applied.

The current scoring contract is deliberately small: command results are matched
by `verificationCommands[].id`, and changed files are compared against
`expectedChangedFiles`. Extra fields should not be added to suite JSON unless
the loader and tests are updated in the same PR.

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

`zero eval run` scores an already-mutated Git worktree. In run mode, use it
after a fixture has been copied somewhere, initialized as a Git repository, and
changed by an agent or by a deterministic local script. Run mode does not copy
fixtures or invoke an agent.

The runner executes each `verificationCommands` entry, collects changed files
with `git status --porcelain`, and emits the task-success report contract below.
When `--workspace` is omitted, the current directory is used.

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

Example using a real local agent command:

```bash
go run ./cmd/zero eval bench \
  --suite internal/agenteval/testdata/sample_suite.json \
  --task document-stream-json-verify-events \
  --work-root /tmp/zero-evals \
  --agent-command zero exec --cwd {workspace} {prompt}
```

Include `{task_id}` when the agent wrapper needs stable per-task logging,
branching, or fixture-specific behavior:

```bash
go run ./cmd/zero eval bench \
  --suite internal/agenteval/testdata/sample_suite.json \
  --work-root /tmp/zero-evals \
  --agent-command zero-agent-wrapper --task {task_id} --workspace {workspace} --prompt {prompt}
```

For deterministic offline testing, point `--agent-command` at a local script
that edits the copied workspace without calling a model:

```bash
go run ./cmd/zero eval bench \
  --suite internal/agenteval/testdata/sample_suite.json \
  --task document-stream-json-verify-events \
  --work-root /tmp/zero-evals \
  --agent-command ./scripts/fake-agent --workspace {workspace} --task {task_id} --prompt {prompt}
```

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
- `results`: one result per verification command, plus `changed_files`.
- `error`: task-selection or report-level error, when present.

Command results include the command ID, display name, command argv, status,
exit code, stdout, stderr, and an optional message. The changed-files result
includes expected, actual, missing, and unexpected files.

## Score Interpretation

Scores are offline quality signals, not pass/fail release gates by default. The
statuses below are produced when a harness supplies captured command results and
changed files.

- `pass`: every verification command exited successfully and the changed files
  matched `expectedChangedFiles`.
- `fail`: at least one command failed or changed files were missing or
  unexpected.
- `blocked`: the harness could not run the task or collect the expected inputs.
- `error`: the suite, task ID, command ID, or captured input could not be
  interpreted.

Real task-success measurement comes from the combination of prompt, fixture,
verification commands, and changed-file expectations. Prefer comparing results
between runs of the same suite revision. Do not compare results across suites
unless the task mix and scoring contract are unchanged.
