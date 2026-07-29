package specialist

import (
	"context"
	"testing"

	"github.com/Gitlawb/zero/internal/streamjson"
	"github.com/Gitlawb/zero/internal/tools"
)

// progressExecutor is an Executor whose child immediately emits one stream-json
// event, so a test can observe whether the progress callback reached it.
func progressExecutor(t *testing.T) Executor {
	t.Helper()
	return Executor{
		BinaryPath:   "/bin/true",
		NewSessionID: func() (string, error) { return "specialist_00000000000000000000000a", nil },
		Load:         func(LoadOptions) (LoadResult, error) { return LoadResult{}, nil },
		RunChild: func(_ context.Context, _ string, _ []string, progress func(streamjson.Event)) (ChildRunResult, error) {
			if progress != nil {
				progress(streamjson.Event{Type: streamjson.EventToolCall, Name: "read_file"})
			}
			return ChildRunResult{Started: true}, nil
		},
	}
}

// THE SECOND CONSTRUCTION PATH, both halves, asserted end to end.
//
// The Task tool forwards its caller's progress callback into TaskRunOptions;
// the plan runner built the same struct and omitted the field, and the
// orchestrate tool never read options.Progress at all. Either half alone leaves
// a plan's children streaming to nobody, which is why they are one change and
// one test: a test that only covered the runner would have passed while the
// tool dropped the callback on the floor, and vice versa.
func TestOrchestrateForwardsProgressToEveryTasksChild(t *testing.T) {
	var events int
	gate := &PostureGate{}
	gate.Set(true)

	tool := &OrchestrateTool{
		PostureActive: gate.Active,
		ParentTools:   []string{"read_file"},
		RunTask: NewPlanRunner(PlanTaskContext{
			Executor:       progressExecutor(t),
			Cwd:            t.TempDir(),
			SpecialistName: "explorer",
		}),
	}

	result := tool.RunWithOptions(context.Background(), map[string]any{
		"name": "p",
		"tasks": []any{
			map[string]any{"id": "a", "prompt": "one"},
			map[string]any{"id": "b", "prompt": "two"},
		},
		"budget": map[string]any{"max_workers": float64(1), "max_tokens": float64(100000)},
	}, tools.RunOptions{
		Progress: func(streamjson.Event) { events++ },
	})

	if result.Status == tools.StatusError {
		t.Fatalf("plan failed: %s", result.Output)
	}
	if events != 2 {
		t.Fatalf("saw %d progress events, want one per task: a plan task's child must stream exactly as a Task sub-agent's does", events)
	}
}

// The runner half on its own: a request carrying a callback must put it on
// TaskRunOptions. Pinned separately so a regression names which half broke.
func TestPlanRunnerForwardsProgress(t *testing.T) {
	var got bool
	runner := NewPlanRunner(PlanTaskContext{
		Executor:       progressExecutor(t),
		Cwd:            t.TempDir(),
		SpecialistName: "explorer",
	})
	if _, err := runner(context.Background(), PlanTaskRequest{
		Task:     Task{ID: "a", Prompt: "x"},
		Tools:    []string{"read_file"},
		Progress: func(streamjson.Event) { got = true },
	}); err != nil {
		t.Fatalf("runner: %v", err)
	}
	if !got {
		t.Fatal("the runner dropped the progress callback; its child streamed to nobody")
	}
}

// A caller that wires no progress is unaffected: nil stays nil rather than
// becoming a non-nil no-op, so the executor's own behaviour is unchanged.
func TestPlanRunnerWithoutProgressPassesNil(t *testing.T) {
	var sawCallback bool
	executor := Executor{
		BinaryPath:   "/bin/true",
		NewSessionID: func() (string, error) { return "specialist_00000000000000000000000a", nil },
		Load:         func(LoadOptions) (LoadResult, error) { return LoadResult{}, nil },
		RunChild: func(_ context.Context, _ string, _ []string, progress func(streamjson.Event)) (ChildRunResult, error) {
			sawCallback = progress != nil
			return ChildRunResult{Started: true}, nil
		},
	}
	runner := NewPlanRunner(PlanTaskContext{Executor: executor, Cwd: t.TempDir(), SpecialistName: "explorer"})
	if _, err := runner(context.Background(), PlanTaskRequest{Task: Task{ID: "a", Prompt: "x"}, Tools: []string{"read_file"}}); err != nil {
		t.Fatalf("runner: %v", err)
	}
	if sawCallback {
		t.Fatal("an unwired plan must hand the executor a nil progress callback, as it always did")
	}
}
