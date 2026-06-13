package agenteval

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestHarnessRunsSelectedTaskFromFixtureAndScoresResult(t *testing.T) {
	suitePath := filepath.Join("testdata", "sample_suite.json")
	suite, err := LoadSuite(suitePath)
	if err != nil {
		t.Fatal(err)
	}
	harness := Harness{
		Materializer: Materializer{},
		Agent: AgentRunnerFunc(func(_ context.Context, input AgentRunInput) AgentRunResult {
			if input.TaskID != "document-stream-json-verify-events" {
				t.Fatalf("agent TaskID = %q", input.TaskID)
			}
			if !strings.Contains(input.Prompt, "stream-json protocol docs") {
				t.Fatalf("agent Prompt = %q", input.Prompt)
			}
			target := filepath.Join(input.WorkspacePath, "docs", "STREAM_JSON_PROTOCOL.md")
			if err := os.WriteFile(target, []byte("updated"), 0o644); err != nil {
				return AgentRunResult{ExitCode: -1, Error: err.Error()}
			}
			return AgentRunResult{ExitCode: 0}
		}),
		Runner: Runner{
			RunCommand: func(_ context.Context, _ string, command Command) CommandResult {
				return CommandResult{ID: command.ID, ExitCode: 0}
			},
		},
	}

	report := harness.Run(context.Background(), suitePath, suite, BenchmarkInput{
		TaskID:   "document-stream-json-verify-events",
		WorkRoot: t.TempDir(),
	})

	if !report.OK {
		t.Fatalf("OK = false; report=%#v", report)
	}
	if report.Contract != ReportContractVersion {
		t.Fatalf("Contract = %q", report.Contract)
	}
	if report.SuiteID != suite.ID {
		t.Fatalf("SuiteID = %q", report.SuiteID)
	}
	if report.Summary != (BenchmarkSummary{TotalTasks: 1, PassedTasks: 1}) {
		t.Fatalf("Summary = %#v", report.Summary)
	}
	if len(report.Tasks) != 1 {
		t.Fatalf("Tasks len = %d", len(report.Tasks))
	}
	taskReport := report.Tasks[0]
	if taskReport.TaskID != "document-stream-json-verify-events" {
		t.Fatalf("TaskID = %q", taskReport.TaskID)
	}
	if taskReport.WorkspacePath == "" || taskReport.FixturePath == "" {
		t.Fatalf("workspace fields were not populated: %#v", taskReport)
	}
	if taskReport.Agent.ExitCode != 0 || taskReport.Agent.Error != "" {
		t.Fatalf("Agent = %#v", taskReport.Agent)
	}
	if taskReport.Report.Status != StatusPass || !taskReport.Report.OK {
		t.Fatalf("Report = %#v", taskReport.Report)
	}
}

func TestHarnessRunsAllTasksWhenTaskIDIsEmpty(t *testing.T) {
	suite := Suite{
		ID:   "suite-a",
		Name: "Suite A",
		Tasks: []Task{
			{
				ID:                   "task-a",
				Name:                 "Task A",
				Prompt:               "change a",
				WorkspaceFixture:     "fixtures/zero-mini",
				ExpectedChangedFiles: []string{"docs/STREAM_JSON_PROTOCOL.md"},
				VerificationCommands: []Command{{ID: "verify-a", Name: "Verify A", Command: []string{"go", "test", "./..."}}},
			},
			{
				ID:                   "task-b",
				Name:                 "Task B",
				Prompt:               "change b",
				WorkspaceFixture:     "fixtures/zero-mini",
				ExpectedChangedFiles: []string{"docs/NPM_WRAPPER_SMOKE.md"},
				VerificationCommands: []Command{{ID: "verify-b", Name: "Verify B", Command: []string{"go", "test", "./..."}}},
			},
		},
	}
	calls := []string{}
	harness := Harness{
		Materializer: Materializer{},
		Agent: AgentRunnerFunc(func(_ context.Context, input AgentRunInput) AgentRunResult {
			calls = append(calls, input.TaskID)
			var target string
			switch input.TaskID {
			case "task-a":
				target = filepath.Join(input.WorkspacePath, "docs", "STREAM_JSON_PROTOCOL.md")
			case "task-b":
				target = filepath.Join(input.WorkspacePath, "docs", "NPM_WRAPPER_SMOKE.md")
			default:
				t.Fatalf("unexpected task %q", input.TaskID)
			}
			if err := os.WriteFile(target, []byte(input.TaskID), 0o644); err != nil {
				return AgentRunResult{ExitCode: -1, Error: err.Error()}
			}
			return AgentRunResult{ExitCode: 0}
		}),
		Runner: Runner{
			RunCommand: func(_ context.Context, _ string, command Command) CommandResult {
				return CommandResult{ID: command.ID, ExitCode: 0}
			},
		},
	}

	report := harness.Run(context.Background(), filepath.Join("testdata", "sample_suite.json"), suite, BenchmarkInput{
		WorkRoot: t.TempDir(),
	})

	if !report.OK {
		t.Fatalf("OK = false; report=%#v", report)
	}
	if report.Summary != (BenchmarkSummary{TotalTasks: 2, PassedTasks: 2}) {
		t.Fatalf("Summary = %#v", report.Summary)
	}
	if strings.Join(calls, ",") != "task-a,task-b" {
		t.Fatalf("agent calls = %#v", calls)
	}
}

func TestHarnessRunsEachSelectedTaskForEachModel(t *testing.T) {
	suite := Suite{
		ID:   "suite-a",
		Name: "Suite A",
		Tasks: []Task{{
			ID:                   "task-a",
			Name:                 "Task A",
			Prompt:               "change a",
			WorkspaceFixture:     "fixtures/zero-mini",
			ExpectedChangedFiles: []string{"docs/STREAM_JSON_PROTOCOL.md"},
			VerificationCommands: []Command{{ID: "verify-a", Name: "Verify A", Command: []string{"go", "test", "./..."}}},
		}},
	}
	calls := []string{}
	harness := Harness{
		Materializer: Materializer{},
		Agent: AgentRunnerFunc(func(_ context.Context, input AgentRunInput) AgentRunResult {
			calls = append(calls, input.Model)
			if input.Model == "" {
				t.Fatal("agent model was empty")
			}
			target := filepath.Join(input.WorkspacePath, "docs", "STREAM_JSON_PROTOCOL.md")
			if err := os.WriteFile(target, []byte(input.Model), 0o644); err != nil {
				return AgentRunResult{ExitCode: -1, Error: err.Error()}
			}
			return AgentRunResult{ExitCode: 0}
		}),
		Runner: Runner{
			RunCommand: func(_ context.Context, _ string, command Command) CommandResult {
				return CommandResult{ID: command.ID, ExitCode: 0}
			},
		},
	}

	report := harness.Run(context.Background(), filepath.Join("testdata", "sample_suite.json"), suite, BenchmarkInput{
		WorkRoot: t.TempDir(),
		Models:   []string{"model-a", "model-b"},
	})

	if !report.OK {
		t.Fatalf("OK = false; report=%#v", report)
	}
	if report.Summary != (BenchmarkSummary{TotalTasks: 2, PassedTasks: 2}) {
		t.Fatalf("Summary = %#v", report.Summary)
	}
	if strings.Join(calls, ",") != "model-a,model-b" {
		t.Fatalf("agent model calls = %#v", calls)
	}
	if report.Tasks[0].Model != "model-a" || report.Tasks[1].Model != "model-b" {
		t.Fatalf("task report models = %#v, %#v", report.Tasks[0].Model, report.Tasks[1].Model)
	}
}

func TestHarnessScoresTraceAndContextChecks(t *testing.T) {
	suite := Suite{
		ID:   "suite-a",
		Name: "Suite A",
		Tasks: []Task{{
			ID:                   "task-a",
			Name:                 "Task A",
			Prompt:               "change a",
			WorkspaceFixture:     "fixtures/zero-mini",
			ExpectedChangedFiles: []string{"docs/STREAM_JSON_PROTOCOL.md"},
			RequiredTraceEvents:  []string{"tool:apply_patch", "tool:read_file"},
			ContextChecks: ContextChecks{
				RequiredFiles:  []string{"docs/STREAM_JSON_PROTOCOL.md"},
				ForbiddenFiles: []string{"tmp/leak.txt"},
			},
			VerificationCommands: []Command{{ID: "verify-a", Name: "Verify A", Command: []string{"go", "test", "./..."}}},
		}},
	}
	harness := Harness{
		Materializer: Materializer{},
		Agent: AgentRunnerFunc(func(_ context.Context, input AgentRunInput) AgentRunResult {
			target := filepath.Join(input.WorkspacePath, "docs", "STREAM_JSON_PROTOCOL.md")
			if err := os.WriteFile(target, []byte("updated"), 0o644); err != nil {
				return AgentRunResult{ExitCode: -1, Error: err.Error()}
			}
			return AgentRunResult{ExitCode: 0, Stdout: "{\"type\":\"tool\",\"name\":\"read_file\"}\n"}
		}),
		Runner: Runner{
			RunCommand: func(_ context.Context, _ string, command Command) CommandResult {
				return CommandResult{ID: command.ID, ExitCode: 0}
			},
		},
	}

	report := harness.Run(context.Background(), filepath.Join("testdata", "sample_suite.json"), suite, BenchmarkInput{
		WorkRoot: t.TempDir(),
	})

	if report.OK || report.Summary.FailedTasks != 1 {
		t.Fatalf("expected trace failure, got %#v", report)
	}
	trace := findResultByID(t, report.Tasks[0].Report.Results, "trace_events")
	if trace.Status != StatusFail || !reflect.DeepEqual(trace.MissingEvents, []string{"tool:apply_patch"}) {
		t.Fatalf("trace result = %#v", trace)
	}
	context := findResultByID(t, report.Tasks[0].Report.Results, "context_checks")
	if context.Status != StatusPass {
		t.Fatalf("context result = %#v", context)
	}
}

func TestHarnessBlocksSelectedTasksWhenAgentIsNil(t *testing.T) {
	suitePath := filepath.Join("testdata", "sample_suite.json")
	suite, err := LoadSuite(suitePath)
	if err != nil {
		t.Fatal(err)
	}
	harness := Harness{Materializer: Materializer{}, Runner: Runner{}}

	report := harness.Run(context.Background(), suitePath, suite, BenchmarkInput{
		TaskID:   "document-stream-json-verify-events",
		WorkRoot: t.TempDir(),
	})

	if report.OK {
		t.Fatalf("OK = true; report=%#v", report)
	}
	if report.Summary != (BenchmarkSummary{TotalTasks: 1, BlockedTasks: 1}) {
		t.Fatalf("Summary = %#v", report.Summary)
	}
	if report.Tasks[0].Agent.Error != "agent command is required" {
		t.Fatalf("Agent.Error = %q", report.Tasks[0].Agent.Error)
	}
	if report.Tasks[0].Agent.ExitCode != -1 {
		t.Fatalf("Agent.ExitCode = %d, want -1", report.Tasks[0].Agent.ExitCode)
	}
	if report.Tasks[0].Report.Status != StatusBlocked {
		t.Fatalf("Report.Status = %q", report.Tasks[0].Report.Status)
	}
}

func TestHarnessBlocksWhenAgentRunFails(t *testing.T) {
	suitePath := filepath.Join("testdata", "sample_suite.json")
	suite, err := LoadSuite(suitePath)
	if err != nil {
		t.Fatal(err)
	}
	harness := Harness{
		Materializer: Materializer{},
		Agent: AgentRunnerFunc(func(context.Context, AgentRunInput) AgentRunResult {
			return AgentRunResult{ExitCode: 2, Stderr: "nope"}
		}),
		Runner: Runner{
			RunCommand: func(context.Context, string, Command) CommandResult {
				t.Fatal("runner should not score after a failed agent run")
				return CommandResult{}
			},
		},
	}

	report := harness.Run(context.Background(), suitePath, suite, BenchmarkInput{
		TaskID:   "document-stream-json-verify-events",
		WorkRoot: t.TempDir(),
	})

	if report.OK {
		t.Fatalf("OK = true; report=%#v", report)
	}
	if report.Summary != (BenchmarkSummary{TotalTasks: 1, BlockedTasks: 1}) {
		t.Fatalf("Summary = %#v", report.Summary)
	}
	if report.Tasks[0].Report.Status != StatusBlocked {
		t.Fatalf("Report.Status = %q", report.Tasks[0].Report.Status)
	}
}

func TestHarnessReportsErrorForUnknownTaskID(t *testing.T) {
	suitePath := filepath.Join("testdata", "sample_suite.json")
	suite, err := LoadSuite(suitePath)
	if err != nil {
		t.Fatal(err)
	}
	harness := Harness{
		Materializer: Materializer{},
		Agent: AgentRunnerFunc(func(context.Context, AgentRunInput) AgentRunResult {
			t.Fatal("agent should not run for an unknown task id")
			return AgentRunResult{}
		}),
		Runner: Runner{},
	}

	report := harness.Run(context.Background(), suitePath, suite, BenchmarkInput{
		TaskID:   "no-such-task",
		WorkRoot: t.TempDir(),
	})

	if report.OK {
		t.Fatalf("OK = true; report=%#v", report)
	}
	if report.Summary != (BenchmarkSummary{TotalTasks: 1, ErrorTasks: 1}) {
		t.Fatalf("Summary = %#v", report.Summary)
	}
	if len(report.Tasks) != 1 || report.Tasks[0].TaskID != "no-such-task" {
		t.Fatalf("Tasks = %#v", report.Tasks)
	}
	if report.Tasks[0].Report.Status != StatusError {
		t.Fatalf("Report.Status = %q", report.Tasks[0].Report.Status)
	}
	if report.Tasks[0].Agent.ExitCode != -1 || !strings.Contains(report.Tasks[0].Agent.Error, "no-such-task") {
		t.Fatalf("Agent should record non-run selection error, got %#v", report.Tasks[0].Agent)
	}
}

func TestHarnessReportsErrorWhenMaterializationFails(t *testing.T) {
	suite := Suite{
		ID:   "suite-mat",
		Name: "Suite Mat",
		Tasks: []Task{{
			ID:                   "missing-fixture",
			Name:                 "Missing fixture",
			Prompt:               "do work",
			WorkspaceFixture:     "fixtures/does-not-exist",
			ExpectedChangedFiles: []string{"x.txt"},
			VerificationCommands: []Command{{ID: "v", Name: "V", Command: []string{"go", "version"}}},
		}},
	}
	agentCalled := false
	harness := Harness{
		Materializer: Materializer{},
		Agent: AgentRunnerFunc(func(context.Context, AgentRunInput) AgentRunResult {
			agentCalled = true
			return AgentRunResult{ExitCode: 0}
		}),
		Runner: Runner{RunCommand: func(context.Context, string, Command) CommandResult {
			t.Fatal("runner should not score when materialization fails")
			return CommandResult{}
		}},
	}

	report := harness.Run(context.Background(), filepath.Join("testdata", "sample_suite.json"), suite, BenchmarkInput{
		WorkRoot: t.TempDir(),
	})

	if report.OK {
		t.Fatalf("OK = true; report=%#v", report)
	}
	if report.Summary != (BenchmarkSummary{TotalTasks: 1, ErrorTasks: 1}) {
		t.Fatalf("Summary = %#v", report.Summary)
	}
	if report.Tasks[0].Report.Status != StatusError {
		t.Fatalf("Report.Status = %q", report.Tasks[0].Report.Status)
	}
	if !strings.Contains(report.Tasks[0].Report.Error, "materialization failed") {
		t.Fatalf("Report.Error = %q", report.Tasks[0].Report.Error)
	}
	if report.Tasks[0].Agent.ExitCode != -1 || report.Tasks[0].Agent.Error == "" {
		t.Fatalf("Agent should record non-run materialization error, got %#v", report.Tasks[0].Agent)
	}
	if agentCalled {
		t.Fatal("agent should not run when materialization fails")
	}
}

func TestHarnessAppliesPerTaskTimeout(t *testing.T) {
	suitePath := filepath.Join("testdata", "sample_suite.json")
	suite, err := LoadSuite(suitePath)
	if err != nil {
		t.Fatal(err)
	}
	var hadDeadline bool
	harness := Harness{
		Materializer: Materializer{},
		Agent: AgentRunnerFunc(func(ctx context.Context, input AgentRunInput) AgentRunResult {
			_, hadDeadline = ctx.Deadline()
			target := filepath.Join(input.WorkspacePath, "docs", "STREAM_JSON_PROTOCOL.md")
			if err := os.WriteFile(target, []byte("updated"), 0o644); err != nil {
				return AgentRunResult{ExitCode: -1, Error: err.Error()}
			}
			return AgentRunResult{ExitCode: 0}
		}),
		Runner: Runner{
			RunCommand: func(_ context.Context, _ string, command Command) CommandResult {
				return CommandResult{ID: command.ID, ExitCode: 0}
			},
		},
	}

	report := harness.Run(context.Background(), suitePath, suite, BenchmarkInput{
		TaskID:   "document-stream-json-verify-events",
		WorkRoot: t.TempDir(),
		Timeout:  30 * time.Second,
	})

	if !report.OK {
		t.Fatalf("OK = false; report=%#v", report)
	}
	if !hadDeadline {
		t.Fatal("expected agent context to carry a deadline when Timeout is set")
	}
}

func TestHarnessTimeoutCancelsBlockedAgent(t *testing.T) {
	suitePath := filepath.Join("testdata", "sample_suite.json")
	suite, err := LoadSuite(suitePath)
	if err != nil {
		t.Fatal(err)
	}
	agentReached := false
	sawCancel := false
	harness := Harness{
		Materializer: Materializer{},
		Agent: AgentRunnerFunc(func(ctx context.Context, _ AgentRunInput) AgentRunResult {
			agentReached = true
			<-ctx.Done()
			sawCancel = ctx.Err() != nil
			return AgentRunResult{ExitCode: -1, Error: ctx.Err().Error()}
		}),
		Runner: Runner{RunCommand: func(context.Context, string, Command) CommandResult {
			t.Fatal("runner should not score after a timed-out agent run")
			return CommandResult{}
		}},
	}

	// The timeout is longer than materialization of the small fixture even on
	// slower Windows CI runners, so the agent is reached before it blocks until
	// the deadline fires.
	report := harness.Run(context.Background(), suitePath, suite, BenchmarkInput{
		TaskID:   "document-stream-json-verify-events",
		WorkRoot: t.TempDir(),
		Timeout:  10 * time.Second,
	})

	if !agentReached {
		t.Fatal("agent was never reached; the timeout fired before the agent ran")
	}
	if !sawCancel {
		t.Fatal("agent ran but never observed context cancellation")
	}
	if report.OK {
		t.Fatalf("OK = true; expected the timeout to fail the task; report=%#v", report)
	}
	if report.Tasks[0].Report.Status != StatusBlocked {
		t.Fatalf("Report.Status = %q, want blocked after agent timeout", report.Tasks[0].Report.Status)
	}
}

func TestHarnessRemovesWorkspacesByDefaultAndKeepsWhenRequested(t *testing.T) {
	suitePath := filepath.Join("testdata", "sample_suite.json")
	suite, err := LoadSuite(suitePath)
	if err != nil {
		t.Fatal(err)
	}
	harness := Harness{
		Materializer: Materializer{},
		Agent: AgentRunnerFunc(func(_ context.Context, input AgentRunInput) AgentRunResult {
			target := filepath.Join(input.WorkspacePath, "docs", "STREAM_JSON_PROTOCOL.md")
			if err := os.WriteFile(target, []byte("updated"), 0o644); err != nil {
				return AgentRunResult{ExitCode: -1, Error: err.Error()}
			}
			return AgentRunResult{ExitCode: 0}
		}),
		Runner: Runner{
			RunCommand: func(_ context.Context, _ string, command Command) CommandResult {
				return CommandResult{ID: command.ID, ExitCode: 0}
			},
		},
	}

	removed := harness.Run(context.Background(), suitePath, suite, BenchmarkInput{
		TaskID:   "document-stream-json-verify-events",
		WorkRoot: t.TempDir(),
	})
	if !removed.OK {
		t.Fatalf("expected passing report, got %#v", removed)
	}
	if _, err := os.Stat(removed.Tasks[0].WorkspacePath); !os.IsNotExist(err) {
		t.Fatalf("default run should remove workspace, stat err=%v", err)
	}

	kept := harness.Run(context.Background(), suitePath, suite, BenchmarkInput{
		TaskID:         "document-stream-json-verify-events",
		WorkRoot:       t.TempDir(),
		KeepWorkspaces: true,
	})
	if !kept.OK {
		t.Fatalf("expected passing report, got %#v", kept)
	}
	if _, err := os.Stat(kept.Tasks[0].WorkspacePath); err != nil {
		t.Fatalf("keep-workspaces should preserve workspace: %v", err)
	}
}
