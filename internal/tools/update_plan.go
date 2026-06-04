package tools

import (
	"context"
	"fmt"
	"strings"
)

type PlanItem struct {
	ID      string
	Content string
	Status  string
	Notes   string
}

type updatePlanTool struct {
	baseTool
	currentPlan []PlanItem
}

func NewUpdatePlanTool() *updatePlanTool {
	return &updatePlanTool{
		baseTool: baseTool{
			name:        "update_plan",
			description: "Create or update the in-memory plan for the current task.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"plan": {Type: "array", Description: "Ordered list of plan items."},
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
	tool.currentPlan = plan
	return okResult(formatPlan(plan))
}

func (tool *updatePlanTool) CurrentPlan() []PlanItem {
	return append([]PlanItem{}, tool.currentPlan...)
}

func (tool *updatePlanTool) ClearPlan() {
	tool.currentPlan = nil
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

		id, err := stringArg(object, "id", "", true)
		if err != nil {
			return nil, fmt.Errorf("plan item %d %s", index+1, err.Error())
		}
		content, err := stringArg(object, "content", "", true)
		if err != nil {
			return nil, fmt.Errorf("plan item %d %s", index+1, err.Error())
		}
		status, err := stringArg(object, "status", "", true)
		if err != nil {
			return nil, fmt.Errorf("plan item %d %s", index+1, err.Error())
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
