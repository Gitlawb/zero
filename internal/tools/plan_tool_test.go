package tools

import (
	"context"
	"strings"
	"testing"
)

func TestUpdatePlanToolStoresAndFormatsPlan(t *testing.T) {
	tool := NewUpdatePlanTool()

	result := tool.Run(context.Background(), map[string]any{
		"plan": []any{
			map[string]any{"id": "1", "content": "First step", "status": "completed"},
			map[string]any{"id": "2", "content": "Second step", "status": "in_progress", "notes": "halfway"},
			map[string]any{"id": "3", "content": "Third step", "status": "pending"},
		},
	})

	if result.Status != StatusOK {
		t.Fatalf("expected ok status, got %s: %s", result.Status, result.Output)
	}
	for _, want := range []string{
		"Current Plan:",
		"1. [completed] First step",
		"2. [in_progress] Second step",
		"Notes: halfway",
		"3. [pending] Third step",
	} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("expected output to contain %q, got %q", want, result.Output)
		}
	}

	plan := tool.CurrentPlan()
	if len(plan) != 3 {
		t.Fatalf("expected 3 plan items, got %d", len(plan))
	}
	plan[0].Content = "mutated"
	if tool.CurrentPlan()[0].Content != "First step" {
		t.Fatalf("CurrentPlan returned mutable internal state")
	}
}

func TestUpdatePlanToolRejectsInvalidStatus(t *testing.T) {
	result := NewUpdatePlanTool().Run(context.Background(), map[string]any{
		"plan": []any{
			map[string]any{"id": "1", "content": "Bad step", "status": "nope"},
		},
	})

	if result.Status != StatusError {
		t.Fatalf("expected error status, got %s", result.Status)
	}
	if !strings.Contains(result.Output, "status must be pending, in_progress, completed, or failed") {
		t.Fatalf("unexpected output: %q", result.Output)
	}
}

func TestUpdatePlanToolClearPlanResetsState(t *testing.T) {
	tool := NewUpdatePlanTool()

	result := tool.Run(context.Background(), map[string]any{
		"plan": []any{
			map[string]any{"id": "1", "content": "First", "status": "pending"},
			map[string]any{"id": "2", "content": "Second", "status": "in_progress"},
		},
	})

	if result.Status != StatusOK {
		t.Fatalf("expected ok status, got %s: %s", result.Status, result.Output)
	}
	if got := tool.CurrentPlan(); len(got) == 0 {
		t.Fatalf("expected stored plan before ClearPlan")
	}

	tool.ClearPlan()
	if got := tool.CurrentPlan(); len(got) != 0 {
		t.Fatalf("expected empty plan after ClearPlan, got %d items", len(got))
	}
	if got := formatPlan(tool.CurrentPlan()); got != "Plan is currently empty." {
		t.Fatalf("expected empty plan formatting after ClearPlan, got %q", got)
	}
}
