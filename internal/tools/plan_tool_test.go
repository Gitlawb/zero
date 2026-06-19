package tools

import (
	"context"
	"strings"
	"sync"
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

func TestUpdatePlanToolAcceptsItemsWithoutID(t *testing.T) {
	tool := NewUpdatePlanTool()
	result := tool.Run(context.Background(), map[string]any{
		"plan": []any{
			map[string]any{"content": "First step", "status": "in_progress"},
			map[string]any{"content": "Second step", "status": "pending"},
		},
	})
	if result.Status != StatusOK {
		t.Fatalf("expected ok status when id omitted, got %s: %s", result.Status, result.Output)
	}
	plan := tool.CurrentPlan()
	if len(plan) != 2 {
		t.Fatalf("expected 2 plan items, got %d", len(plan))
	}
	if plan[0].ID != "1" || plan[1].ID != "2" {
		t.Fatalf("expected ids auto-derived from index, got %q,%q", plan[0].ID, plan[1].ID)
	}
}

func TestUpdatePlanToolDefaultsStatusToPending(t *testing.T) {
	tool := NewUpdatePlanTool()
	result := tool.Run(context.Background(), map[string]any{
		"plan": []any{
			map[string]any{"content": "Only content"},
		},
	})
	if result.Status != StatusOK {
		t.Fatalf("expected ok status when status omitted, got %s: %s", result.Status, result.Output)
	}
	if got := tool.CurrentPlan(); got[0].Status != "pending" {
		t.Fatalf("expected status to default to pending, got %q", got[0].Status)
	}
}

func TestUpdatePlanToolRequiresContent(t *testing.T) {
	result := NewUpdatePlanTool().Run(context.Background(), map[string]any{
		"plan": []any{map[string]any{"status": "pending"}},
	})
	if result.Status != StatusError {
		t.Fatalf("expected error when content missing, got %s", result.Status)
	}
	if !strings.Contains(result.Output, "content is required") {
		t.Fatalf("unexpected output: %q", result.Output)
	}
}

func TestUpdatePlanToolConcurrentRunAndRead(t *testing.T) {
	tool := NewUpdatePlanTool()
	args := map[string]any{
		"plan": []any{
			map[string]any{"content": "First step", "status": "in_progress"},
			map[string]any{"content": "Second step", "status": "pending"},
		},
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(3)
		go func() { defer wg.Done(); tool.Run(context.Background(), args) }()
		go func() { defer wg.Done(); _ = tool.CurrentPlan() }()
		go func() { defer wg.Done(); tool.ClearPlan() }()
	}
	wg.Wait()
}

func TestUpdatePlanToolAdvertisesItemSchema(t *testing.T) {
	plan := NewUpdatePlanTool().Parameters().Properties["plan"]
	if plan.Type != "array" {
		t.Fatalf("expected plan to be an array, got %q", plan.Type)
	}
	if plan.Items == nil {
		t.Fatal("plan should have a structured Items schema")
	}
	if plan.Items.Type != "object" {
		t.Fatalf("plan items should be objects, got %q", plan.Items.Type)
	}
	contentProp, ok := plan.Items.Properties["content"]
	if !ok {
		t.Fatal("plan items should have a 'content' property")
	}
	if contentProp.Type != "string" {
		t.Fatalf("content property should be string, got %q", contentProp.Type)
	}
	statusProp, ok := plan.Items.Properties["status"]
	if !ok {
		t.Fatal("plan items should have a 'status' property")
	}
	if len(statusProp.Enum) == 0 {
		t.Fatal("status property should have an enum")
	}
	if len(plan.Items.Required) == 0 || plan.Items.Required[0] != "content" {
		t.Fatalf("plan items should require 'content', got %v", plan.Items.Required)
	}
}
