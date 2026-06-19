package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

type PlanItem struct {
	ID      string
	Content string
	Status  string
	Notes   string
}

type updatePlanTool struct {
	baseTool
	// mu guards currentPlan: Run() writes it on the agent goroutine while
	// CurrentPlan()/ClearPlan() are called from the TUI goroutine (e.g. /plan).
	mu          sync.Mutex
	currentPlan []PlanItem
}

func NewUpdatePlanTool() *updatePlanTool {
	return &updatePlanTool{
		baseTool: baseTool{
			name: "update_plan",
			description: "Create or update the in-memory plan for the current task. " +
				"Pass the full ordered list of steps each call; it replaces the previous plan. " +
				"Each item needs a `content` string; `status` defaults to \"pending\" and `id` is " +
				"auto-numbered, so you only need to supply `content` (and `status` as the task progresses).",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"plan": {
						Type:        "array",
						Description: "Ordered list of plan items, replacing any previous plan.",
						Items: &PropertySchema{
							Type: "object",
							Properties: map[string]PropertySchema{
								"content": {
									Type:        "string",
									Description: "The plan step description.",
								},
								"status": {
									Type:        "string",
									Description: "Status of this step.",
									Enum:        []string{"pending", "in_progress", "completed", "failed"},
								},
								"notes": {
									Type:        "string",
									Description: "Optional notes for this step.",
								},
							},
							Required: []string{"content"},
						},
					},
				},
				Required:             []string{"plan"},
				AdditionalProperties: false,
			},
			safety: readOnlySafety("Updates in-memory planning state only."),
		},
	}
}

func (tool *updatePlanTool) Run(_ context.Context, args map[string]any) Result {
	plan, err := parsePlanItems(args["plan"])
	if err != nil {
		return errorResult("Error: Invalid arguments for update_plan: " + err.Error())
	}
	tool.mu.Lock()
	tool.currentPlan = plan
	tool.mu.Unlock()
	return okResult(formatPlan(plan))
}

func (tool *updatePlanTool) CurrentPlan() []PlanItem {
	tool.mu.Lock()
	defer tool.mu.Unlock()
	return append([]PlanItem{}, tool.currentPlan...)
}

func (tool *updatePlanTool) ClearPlan() {
	tool.mu.Lock()
	tool.currentPlan = nil
	tool.mu.Unlock()
}

func parsePlanItems(value any) ([]PlanItem, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("plan must be an array")
	}

	plan := make([]PlanItem, 0, len(items))
	for index, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("plan item %d must be an object", index+1)
		}

		content, err := stringArg(object, "content", "", true)
		if err != nil {
			return nil, fmt.Errorf("plan item %d %s", index+1, err.Error())
		}
		// id is optional: weaker models can't reliably mint stable ids, and the
		// plan is displayed by 1-based position anyway. Auto-number when omitted.
		id, err := stringArgWithEmpty(object, "id", fmt.Sprintf("%d", index+1), false, true)
		if err != nil {
			return nil, fmt.Errorf("plan item %d %s", index+1, err.Error())
		}
		if id == "" {
			id = fmt.Sprintf("%d", index+1)
		}
		// status is optional and defaults to pending.
		status, err := stringArgWithEmpty(object, "status", "pending", false, true)
		if err != nil {
			return nil, fmt.Errorf("plan item %d %s", index+1, err.Error())
		}
		if status == "" {
			status = "pending"
		}
		if !isPlanStatus(status) {
			return nil, fmt.Errorf("plan item %d status must be pending, in_progress, completed, or failed", index+1)
		}
		notes, err := stringArgWithEmpty(object, "notes", "", false, true)
		if err != nil {
			return nil, fmt.Errorf("plan item %d %s", index+1, err.Error())
		}

		plan = append(plan, PlanItem{
			ID:      id,
			Content: content,
			Status:  status,
			Notes:   notes,
		})
	}
	return plan, nil
}

func isPlanStatus(status string) bool {
	return status == "pending" || status == "in_progress" || status == "completed" || status == "failed"
}

func formatPlan(plan []PlanItem) string {
	if len(plan) == 0 {
		return "Plan is currently empty."
	}

	lines := make([]string, 0, len(plan))
	for index, item := range plan {
		line := fmt.Sprintf("%d. [%s] %s", index+1, item.Status, item.Content)
		if item.Notes != "" {
			line += "\n   Notes: " + item.Notes
		}
		lines = append(lines, line)
	}
	return "Current Plan:\n" + strings.Join(lines, "\n")
}
