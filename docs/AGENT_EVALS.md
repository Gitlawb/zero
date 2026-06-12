# Offline Agent Evals

Zero agent evals are maintainer fixtures for checking coding-agent behavior
without calling a live model. They describe a task, the files the agent is
expected to change, the commands that should verify the result, and the scoring
rules an offline harness can apply to a captured run.

These fixtures are intentionally local-first. They do not prove provider quality
or live model execution by themselves; they give tests and future CLI work a
stable sample suite to validate, run against copied workspaces, and score from
saved outputs.

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

`zero eval` defaults to validate mode. It parses the suite, rejects schema or
contract errors, and reports the number of tasks and checks. It does not copy
fixtures, invoke an agent, or execute verification commands.

```bash
go run ./cmd/zero eval --suite internal/agenteval/testdata/sample_suite.json
```

Use JSON output when another local tool needs the validation summary:

```bash
go run ./cmd/zero eval --suite internal/agenteval/testdata/sample_suite.json --json
```

`zero eval run` scores one local workspace. It does not invoke the agent yet;
point it at a Git worktree where a fixture has already been copied and a task
has already been attempted. The runner executes each `verificationCommands`
entry, collects changed files with `git status --porcelain`, and emits the
task-success report contract below. `--workspace` is required: it must point at
the prepared fixture worktree, never the current directory, so the suite's
verification commands (`go test`, `git`, …) don't run against your real repo.

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
