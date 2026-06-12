# Offline Agent Evals

Zero agent evals are maintainer fixtures for checking coding-agent behavior
without calling a live model. They describe a task, the files the agent is
expected to change, the commands that should verify the result, and the scoring
rules an offline harness can apply to a captured run.

These fixtures are intentionally contract-shaped. They do not prove provider
quality or live model execution by themselves; they give tests and future CLI
work a stable sample suite to parse, validate, and score against saved outputs.

## Suite Format

Sample suites live under `internal/agenteval/testdata/`.

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

## Run Locally

Validate and summarize a suite through the CLI:

```bash
go run ./cmd/zero eval --suite internal/agenteval/testdata/sample_suite.json
```

For JSON output:

```bash
go run ./cmd/zero eval --suite internal/agenteval/testdata/sample_suite.json --json
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

## Score Interpretation

Scores are offline quality signals, not pass/fail release gates by default. The
current `zero eval` command validates and summarizes suite files only; the
statuses below are produced by the `internal/agenteval.Score` library API when a
future harness supplies captured command results and changed files.

- `pass`: every verification command exited successfully and the changed files
  matched `expectedChangedFiles`.
- `fail`: at least one command failed or changed files were missing or
  unexpected.
- `blocked`: the harness could not run the task or collect the expected inputs.
- `error`: the suite, task ID, command ID, or captured input could not be
  interpreted.

Prefer comparing results between runs of the same suite revision. Do not compare
results across suites unless the task mix and scoring contract are unchanged.
