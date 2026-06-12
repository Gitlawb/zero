# Agent Eval Benchmark Harness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a local, offline-testable benchmark harness that copies eval fixtures, runs an agent command per task, scores the resulting workspace, and reports aggregate results.

**Architecture:** Keep `internal/agenteval.Runner` as the scoring primitive. Add a `Harness` layer that materializes fixture workspaces into a work directory, initializes a clean Git baseline, invokes an injectable `AgentRunner`, then calls `Runner.Run`. The CLI exposes this as `zero eval bench`, while `zero eval run` remains the lower-level scorer for an already-mutated workspace.

**Tech Stack:** Go 1.24 standard library, existing `internal/agenteval`, existing `internal/cli`, Git CLI for workspace baselines.

---

## File Structure

- Create `internal/agenteval/materialize.go`: fixture path resolution, directory copy, Git init/baseline helpers.
- Create `internal/agenteval/materialize_test.go`: fixture copy and Git baseline tests.
- Create `internal/agenteval/agent_command.go`: command-template based agent runner with `{prompt}`, `{workspace}`, `{task_id}` placeholders.
- Create `internal/agenteval/agent_command_test.go`: placeholder substitution, cwd, exit/error behavior.
- Create `internal/agenteval/benchmark.go`: benchmark orchestration and aggregate report.
- Create `internal/agenteval/benchmark_test.go`: end-to-end harness with fake agent runner and sample fixtures.
- Modify `internal/cli/agent_eval.go`: parse `zero eval bench` and wire `Harness`.
- Modify `internal/cli/agent_eval_test.go`: bench parsing, JSON/text output, report writing.
- Modify `docs/AGENT_EVALS.md`: document `bench` mode and real local command examples.

## Task 1: Fixture Materializer

**Files:**
- Create: `internal/agenteval/materialize.go`
- Create: `internal/agenteval/materialize_test.go`

- [ ] **Step 1: Write failing tests**

```go
func TestMaterializeTaskCopiesFixtureAndInitializesGit(t *testing.T) {
	suitePath := filepath.Join("testdata", "sample_suite.json")
	suite, err := LoadSuite(suitePath)
	if err != nil {
		t.Fatal(err)
	}
	task := suite.Tasks[0]
	workRoot := t.TempDir()

	workspace, err := Materializer{}.MaterializeTask(context.Background(), suitePath, task, MaterializeInput{WorkRoot: workRoot})
	if err != nil {
		t.Fatalf("MaterializeTask: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace.Path, "go.mod")); err != nil {
		t.Fatalf("fixture was not copied: %v", err)
	}
	if output, err := exec.Command("git", "-C", workspace.Path, "status", "--porcelain").CombinedOutput(); err != nil || strings.TrimSpace(string(output)) != "" {
		t.Fatalf("workspace baseline is dirty: err=%v output=%s", err, output)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agenteval -run TestMaterializeTask -count=1`
Expected: FAIL because `Materializer` is undefined.

- [ ] **Step 3: Implement minimal materializer**

Create a `Materializer` with:
- `MaterializeTask(ctx, suitePath string, task Task, input MaterializeInput) (Workspace, error)`
- `MaterializeInput{WorkRoot string}`
- `Workspace{Path string, TaskID string, FixturePath string}`
- Resolve relative `task.WorkspaceFixture` from `filepath.Dir(suitePath)`.
- Copy directories recursively using stdlib only.
- Skip `.git` while copying.
- Run `git init`, `git add .`, and `git commit -m "baseline"` with local user config/env.
- Return clear errors for missing fixture, absolute fixture escapes, and empty work root.

- [ ] **Step 4: Verify**

Run:
```bash
go test ./internal/agenteval -run 'TestMaterializeTask|TestMaterializer' -count=1
```

## Task 2: Agent Command Runner

**Files:**
- Create: `internal/agenteval/agent_command.go`
- Create: `internal/agenteval/agent_command_test.go`

- [ ] **Step 1: Write failing tests**

```go
func TestCommandAgentRunnerExpandsPlaceholders(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "agent.bat")
	if err := os.WriteFile(script, []byte("@echo %1|%2|%3> out.txt\r\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := CommandAgentRunner{Command: []string{script, "{task_id}", "{workspace}", "{prompt}"}}
	result := runner.Run(context.Background(), AgentRunInput{TaskID: "task-a", WorkspacePath: dir, Prompt: "fix bug"})
	if result.ExitCode != 0 || result.Error != "" {
		t.Fatalf("Run = %#v", result)
	}
	data, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "task-a") || !strings.Contains(string(data), dir) || !strings.Contains(string(data), "fix bug") {
		t.Fatalf("placeholders not expanded: %q", data)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agenteval -run TestCommandAgentRunner -count=1`
Expected: FAIL because `CommandAgentRunner` is undefined.

- [ ] **Step 3: Implement runner**

Add:
- `type AgentRunInput struct { TaskID, Prompt, WorkspacePath string }`
- `type AgentRunResult struct { ExitCode int; Stdout, Stderr, Error string }`
- `type AgentRunner interface { Run(context.Context, AgentRunInput) AgentRunResult }`
- `type CommandAgentRunner struct { Command []string }`
- `Run` executes without shell interpolation, with `cmd.Dir = WorkspacePath`.
- Replace `{prompt}`, `{workspace}`, `{task_id}` in every arg.
- Empty command returns `ExitCode:-1` and `Error:"agent command is required"`.

- [ ] **Step 4: Verify**

Run: `go test ./internal/agenteval -run TestCommandAgentRunner -count=1`

## Task 3: Benchmark Harness

**Files:**
- Create: `internal/agenteval/benchmark.go`
- Create: `internal/agenteval/benchmark_test.go`

- [ ] **Step 1: Write failing tests**

```go
func TestHarnessRunsTaskFromFixtureAndScoresResult(t *testing.T) {
	suitePath := filepath.Join("testdata", "sample_suite.json")
	suite, err := LoadSuite(suitePath)
	if err != nil {
		t.Fatal(err)
	}
	harness := Harness{
		Materializer: Materializer{},
		Agent: agentRunnerFunc(func(ctx context.Context, input AgentRunInput) AgentRunResult {
			target := filepath.Join(input.WorkspacePath, "docs", "STREAM_JSON_PROTOCOL.md")
			err := os.WriteFile(target, []byte("updated"), 0o644)
			if err != nil {
				return AgentRunResult{ExitCode: -1, Error: err.Error()}
			}
			return AgentRunResult{ExitCode: 0}
		}),
		Runner: Runner{},
	}
	report := harness.Run(context.Background(), suitePath, suite, BenchmarkInput{
		TaskID:   "document-stream-json-verify-events",
		WorkRoot: t.TempDir(),
	})
	if report.Summary.TotalTasks != 1 || report.Summary.PassedTasks != 1 || !report.OK {
		t.Fatalf("report = %#v", report)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agenteval -run TestHarness -count=1`
Expected: FAIL because `Harness` is undefined.

- [ ] **Step 3: Implement harness**

Add:
- `BenchmarkInput{TaskID, WorkRoot string; KeepWorkspaces bool}`
- `BenchmarkReport{Contract, SuiteID, OK, Summary, Tasks}`
- `BenchmarkTaskReport{TaskID, WorkspacePath, FixturePath, Agent AgentRunResult, Report Report}`
- `BenchmarkSummary{TotalTasks, PassedTasks, FailedTasks, BlockedTasks, ErrorTasks int}`
- `Harness{Materializer Materializer; Agent AgentRunner; Runner Runner}`
- If no `Agent`, produce blocked task reports with message `agent command is required`.
- Select one task when `TaskID` set, otherwise all tasks.
- For each task: materialize, run agent, if agent fails mark blocked; otherwise score with `Runner.Run`.
- Remove each materialized task workspace after scoring unless `BenchmarkInput.KeepWorkspaces` is true.

- [ ] **Step 4: Verify**

Run: `go test ./internal/agenteval -run 'TestHarness|TestBenchmark' -count=1`

## Task 4: CLI `zero eval bench`

**Files:**
- Modify: `internal/cli/agent_eval.go`
- Modify: `internal/cli/agent_eval_test.go`

- [ ] **Step 1: Write failing tests**

```go
func TestRunEvalBenchJSONModePassesHarnessOptions(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps([]string{
		"eval", "bench",
		"--suite", "evals/context.json",
		"--task", "edit-reader",
		"--work-root", "D:\\tmp\\zero-evals",
		"--agent-command", "zero", "exec", "{prompt}",
		"--json",
	}, &stdout, &stderr, appDeps{
		runAgentEval: func(ctx context.Context, options agentEvalOptions) (agentEvalReport, error) {
			if options.Mode != "bench" || options.TaskID != "edit-reader" || options.WorkRoot == "" || len(options.AgentCommand) != 3 {
				t.Fatalf("unexpected bench options: %#v", options)
			}
			return agentEvalReport{Suite: "quality-context", Status: "pass", OK: true, Total: 1, Passed: 1}, nil
		},
	})
	if exitCode != exitSuccess {
		t.Fatalf("exit=%d stderr=%s", exitCode, stderr.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli -run TestRunEvalBench -count=1`
Expected: FAIL because bench mode is not parsed.

- [ ] **Step 3: Implement CLI**

Add parser support:
- Modes: `validate`, `run`, `bench`
- Bench flags: `--work-root <path>`, `--agent-command <argv...>`, `--keep-workspaces`
- `--task`, `--report-dir`, `--json` work in bench mode.
- `--workspace` remains run-only.
- Default work root for bench: temp dir with prefix `zero-eval-`.
- Default `AgentCommand` empty -> harness report blocked rather than usage error.
- Convert benchmark aggregate to existing CLI report shape.

- [ ] **Step 4: Verify**

Run:
```bash
go test ./internal/cli -run 'TestRunEvalBench|TestRunEvalRun' -count=1
```

## Task 5: Docs and Manual Examples

**Files:**
- Modify: `docs/AGENT_EVALS.md`

- [ ] **Step 1: Document modes**

Add a `bench` subsection:
- `validate`: schema only
- `run`: score an already-mutated worktree
- `bench`: copy fixture, run agent command, score result

- [ ] **Step 2: Include examples**

Document:
```bash
go run ./cmd/zero eval bench \
  --suite internal/agenteval/testdata/sample_suite.json \
  --task document-stream-json-verify-events \
  --work-root /tmp/zero-evals \
  --agent-command zero exec --cwd {workspace} {prompt}
```

Also document a deterministic fake-agent example for local testing.

- [ ] **Step 3: Verify**

Run:
```bash
go run ./cmd/zero eval --suite internal/agenteval/testdata/sample_suite.json
go test ./...
```

## Task 6: Integration Validation

**Files:**
- Modify as needed only in files above.

- [ ] **Step 1: Run focused validation**

```bash
go test -count=1 ./internal/agenteval ./internal/cli
```

- [ ] **Step 2: Run fixture validation**

```bash
go test ./...
```

from `internal/agenteval/testdata/fixtures/zero-mini`.

- [ ] **Step 3: Run full repo validation**

```bash
gofmt -l internal/agenteval internal/cli
git diff --check
go test ./...
go run ./cmd/zero-release build
go run ./cmd/zero-release smoke
```
