package perfbench

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func sampleTaskSet() TaskSet {
	return TaskSet{
		ID:   "terminal-bench-mini",
		Name: "Terminal-Bench (mini)",
		Tasks: []BenchTask{
			{ID: "t1", Name: "fix failing test", Prompt: "make the test pass", WorkspaceFixture: "fixtures/t1"},
			{ID: "t2", Name: "add flag", Prompt: "add a --json flag", WorkspaceFixture: "fixtures/t2"},
		},
	}
}

func TestRunTasksRecordsModelCommitAndSelfCorrect(t *testing.T) {
	config := TaskConfig{
		Model:       "test-model",
		Mode:        "build",
		SelfCorrect: true,
		Version:     "1.2.3",
		Commit:      "abc1234",
		Runner: func(_ context.Context, task BenchTask, rc RunContext) TaskOutcome {
			if !rc.SelfCorrect {
				t.Fatalf("runner should see SelfCorrect=true from config")
			}
			if rc.Model != "test-model" {
				t.Fatalf("runner model = %q, want test-model", rc.Model)
			}
			return TaskOutcome{Passed: true}
		},
	}

	result, err := RunTasks(context.Background(), sampleTaskSet(), config)
	if err != nil {
		t.Fatalf("RunTasks returned error: %v", err)
	}

	if result.Model != "test-model" {
		t.Fatalf("result.Model = %q, want test-model", result.Model)
	}
	if result.Commit != "abc1234" {
		t.Fatalf("result.Commit = %q, want abc1234", result.Commit)
	}
	if result.Version != "1.2.3" {
		t.Fatalf("result.Version = %q, want 1.2.3", result.Version)
	}
	if !result.SelfCorrect {
		t.Fatalf("result.SelfCorrect = false, want true")
	}
	if result.Mode != "build" {
		t.Fatalf("result.Mode = %q, want build", result.Mode)
	}
	if result.TasksAttempted != 2 || result.TasksPassed != 2 {
		t.Fatalf("attempted/passed = %d/%d, want 2/2", result.TasksAttempted, result.TasksPassed)
	}
	if result.Suite != "terminal-bench-mini" {
		t.Fatalf("result.Suite = %q, want terminal-bench-mini", result.Suite)
	}
	if strings.TrimSpace(result.Date) == "" {
		t.Fatalf("result.Date must be recorded, got empty")
	}
	if result.SchemaVersion != TaskSchemaVersion {
		t.Fatalf("schema version = %d, want %d", result.SchemaVersion, TaskSchemaVersion)
	}
	if len(result.Tasks) != 2 {
		t.Fatalf("per-task results = %d, want 2", len(result.Tasks))
	}
}

func TestRunTasksCountsPassesAndFailures(t *testing.T) {
	config := TaskConfig{
		Model: "m",
		Runner: func(_ context.Context, task BenchTask, _ RunContext) TaskOutcome {
			if task.ID == "t1" {
				return TaskOutcome{Passed: true}
			}
			return TaskOutcome{Passed: false, Detail: "verification failed"}
		},
	}

	result, err := RunTasks(context.Background(), sampleTaskSet(), config)
	if err != nil {
		t.Fatalf("RunTasks returned error: %v", err)
	}
	if result.TasksAttempted != 2 || result.TasksPassed != 1 {
		t.Fatalf("attempted/passed = %d/%d, want 2/1", result.TasksAttempted, result.TasksPassed)
	}
	if result.PassRate < 0.49 || result.PassRate > 0.51 {
		t.Fatalf("pass rate = %v, want ~0.5", result.PassRate)
	}
	var t2 *TaskResult
	for i := range result.Tasks {
		if result.Tasks[i].ID == "t2" {
			t2 = &result.Tasks[i]
		}
	}
	if t2 == nil || t2.Passed || t2.Detail != "verification failed" {
		t.Fatalf("t2 result = %#v, want failed with detail", t2)
	}
}

func TestRunTasksRecordsRunnerError(t *testing.T) {
	config := TaskConfig{
		Model: "m",
		Runner: func(_ context.Context, task BenchTask, _ RunContext) TaskOutcome {
			return TaskOutcome{Err: errors.New("zero exec exited 1")}
		},
	}

	result, err := RunTasks(context.Background(), sampleTaskSet(), config)
	if err != nil {
		t.Fatalf("RunTasks must not abort on a single task error: %v", err)
	}
	// A runner error counts as a non-pass (attempted but not passed) and is
	// recorded as the task detail, never silently dropped.
	if result.TasksPassed != 0 {
		t.Fatalf("errored tasks must not count as passed: %#v", result)
	}
	if result.Errors != 2 {
		t.Fatalf("errors = %d, want 2", result.Errors)
	}
	if !strings.Contains(result.Tasks[0].Detail, "zero exec exited 1") {
		t.Fatalf("task detail should carry the runner error, got %q", result.Tasks[0].Detail)
	}
}

func TestRunTasksRejectsEmptyTaskSet(t *testing.T) {
	_, err := RunTasks(context.Background(), TaskSet{ID: "empty"}, TaskConfig{Model: "m", Runner: passingRunner})
	if err == nil {
		t.Fatalf("RunTasks should reject an empty task set")
	}
}

func TestRunTasksRequiresModel(t *testing.T) {
	_, err := RunTasks(context.Background(), sampleTaskSet(), TaskConfig{Runner: passingRunner})
	if err == nil || !strings.Contains(err.Error(), "model") {
		t.Fatalf("RunTasks should require a model, got err=%v", err)
	}
}

func TestRunTasksRequiresRunner(t *testing.T) {
	_, err := RunTasks(context.Background(), sampleTaskSet(), TaskConfig{Model: "m"})
	if err == nil || !strings.Contains(err.Error(), "runner") {
		t.Fatalf("RunTasks should require a runner, got err=%v", err)
	}
}

func TestFormatTaskSummaryIsHonestAboutModelAndSelfCorrect(t *testing.T) {
	result := TaskRunResult{
		SchemaVersion:  TaskSchemaVersion,
		Suite:          "terminal-bench-mini",
		Model:          "test-model",
		Mode:           "build",
		SelfCorrect:    true,
		Version:        "1.2.3",
		Commit:         "abc1234",
		Date:           "2026-06-12T00:00:00Z",
		TasksAttempted: 2,
		TasksPassed:    1,
		PassRate:       0.5,
		Tasks: []TaskResult{
			{ID: "t1", Name: "fix", Passed: true},
			{ID: "t2", Name: "flag", Passed: false, Detail: "nope"},
		},
	}

	summary := FormatTaskSummary(result)
	for _, want := range []string{
		"terminal-bench-mini",
		"model: test-model",
		"self-correct: on",
		"commit abc1234",
		"1/2",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, summary)
		}
	}
}

func TestFormatTaskSummarySelfCorrectOff(t *testing.T) {
	summary := FormatTaskSummary(TaskRunResult{
		Suite: "s", Model: "m", SelfCorrect: false,
		TasksAttempted: 1, TasksPassed: 0,
		Tasks: []TaskResult{{ID: "t1", Passed: false}},
	})
	if !strings.Contains(summary, "self-correct: off") {
		t.Fatalf("summary should report self-correct off:\n%s", summary)
	}
}

func passingRunner(context.Context, BenchTask, RunContext) TaskOutcome {
	return TaskOutcome{Passed: true}
}
