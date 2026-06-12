# Agent Eval D-Level Upgrades Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Upgrade the local agent eval harness from a smoke benchmark into a quality signal that can compare models, score traces, catch context mistakes, and preserve regression artifacts.

**Architecture:** Keep the existing `internal/agenteval` harness as the core. Add small schema fields to `Task`, score them through `Score` and `Harness.Run`, then expose model matrix and full benchmark report data through `zero eval bench`. Avoid provider calls or UI work.

**Tech Stack:** Go 1.24, standard library JSON/process/git helpers, existing `go test ./...` validation.

---

### Task 1: Suite Schema And Rubric Checks

**Files:**
- Modify: `internal/agenteval/suite.go`
- Modify: `internal/agenteval/score.go`
- Test: `internal/agenteval/agenteval_test.go`

- [ ] **Step 1: Write failing schema/rubric tests**

Add tests that load and normalize `tags`, `difficulty`, and `forbiddenChangedFiles`, reject malformed forbidden paths, and fail scoring when a forbidden file is touched.

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/agenteval -run "LoadSuite|Validate|Forbidden" -count=1`

- [ ] **Step 3: Implement minimal schema and scoring**

Add these fields:

```go
Tags                  []string `json:"tags,omitempty"`
Difficulty            string   `json:"difficulty,omitempty"`
ForbiddenChangedFiles []string `json:"forbiddenChangedFiles,omitempty"`
```

Normalize file lists, validate duplicate/malformed entries, and add a `forbidden_changed_files` result.

- [ ] **Step 4: Verify green**

Run: `go test ./internal/agenteval -count=1`

### Task 2: Agent Trace Scoring

**Files:**
- Create: `internal/agenteval/trace.go`
- Test: `internal/agenteval/trace_test.go`
- Modify: `internal/agenteval/benchmark.go`

- [ ] **Step 1: Write failing trace tests**

Cover JSONL events such as:

```json
{"type":"tool","name":"read_file"}
{"event":"verify","name":"go-test"}
```

Require deterministic event keys like `tool:read_file` and `verify:go-test`.

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/agenteval -run Trace -count=1`

- [ ] **Step 3: Implement parser and benchmark scoring**

Parse agent stdout line by line, ignore non-JSON noise, and append a `trace` result when a task declares `requiredTraceEvents`.

- [ ] **Step 4: Verify green**

Run: `go test ./internal/agenteval -count=1`

### Task 3: Context Quality Checks

**Files:**
- Create: `internal/agenteval/context_checks.go`
- Test: `internal/agenteval/context_checks_test.go`
- Modify: `internal/agenteval/benchmark.go`

- [ ] **Step 1: Write failing context tests**

Cover a task that declares:

```json
"contextChecks": {
  "requiredFiles": ["docs/STREAM_JSON_PROTOCOL.md"],
  "forbiddenFiles": ["node_modules/cache.txt"]
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/agenteval -run Context -count=1`

- [ ] **Step 3: Implement fixture/workspace context checks**

Check required files exist under the materialized workspace and forbidden files do not. Append a `context` result before summary finalization.

- [ ] **Step 4: Verify green**

Run: `go test ./internal/agenteval -count=1`

### Task 4: Model Matrix Benchmarking

**Files:**
- Modify: `internal/agenteval/agent_command.go`
- Modify: `internal/agenteval/benchmark.go`
- Test: `internal/agenteval/agent_command_test.go`
- Test: `internal/agenteval/benchmark_test.go`

- [ ] **Step 1: Write failing model-matrix tests**

Assert `{model}` expands in agent command argv and `BenchmarkInput{Models: []string{"a","b"}}` runs every task for each model.

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/agenteval -run "Model|Harness" -count=1`

- [ ] **Step 3: Implement model propagation**

Add `Model` to `AgentRunInput` and `BenchmarkTaskReport`; add `Models []string` to `BenchmarkInput`; loop task/model pairs deterministically.

- [ ] **Step 4: Verify green**

Run: `go test ./internal/agenteval -count=1`

### Task 5: CLI Flags And Regression Artifacts

**Files:**
- Modify: `internal/cli/agent_eval.go`
- Test: `internal/cli/agent_eval_test.go`

- [ ] **Step 1: Write failing CLI tests**

Cover repeated `--model`, comma-separated `--models`, help text, and report-dir JSON containing nested benchmark detail.

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/cli -run Eval -count=1`

- [ ] **Step 3: Implement CLI wiring**

Pass models into `agenteval.BenchmarkInput`, include benchmark detail in `agentEvalReport`, and prefix failure IDs with model when present.

- [ ] **Step 4: Verify green**

Run: `go test ./internal/cli -count=1`

### Task 6: Suite Expansion And Docs

**Files:**
- Modify: `internal/agenteval/testdata/sample_suite.json`
- Modify: `docs/AGENT_EVALS.md`

- [ ] **Step 1: Write failing suite expectation**

Update the sample suite test to require richer coverage count and metadata fields.

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/agenteval -run SampleSuite -count=1`

- [ ] **Step 3: Expand the fixture suite and docs**

Add more tasks using the existing `zero-mini` fixture, plus examples for `--model`, `{model}`, `requiredTraceEvents`, `contextChecks`, and report artifacts.

- [ ] **Step 4: Verify green**

Run: `go test ./internal/agenteval ./internal/cli -count=1`

### Final Verification

- [ ] Run `gofmt -l internal/agenteval internal/cli`
- [ ] Run `git diff --check`
- [ ] Run `go vet ./...`
- [ ] Run `go test ./...`
- [ ] Run `go run ./cmd/zero eval --suite internal/agenteval/testdata/sample_suite.json`
- [ ] Run `go run ./cmd/zero eval bench --suite internal/agenteval/testdata/sample_suite.json --task document-stream-json-verify-events --model test-model --json --agent-command powershell -NoProfile -Command "& { param(`$ws) Set-Content -LiteralPath (Join-Path `$ws 'docs/STREAM_JSON_PROTOCOL.md') -Value updated; Write-Output '{\"type\":\"tool\",\"name\":\"read_file\"}' }" "{workspace}"`
- [ ] Run `go run ./cmd/zero-release build`
- [ ] Commit and push to `feat/agent-eval-harness`
