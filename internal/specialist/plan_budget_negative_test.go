package specialist

import (
	"strings"
	"testing"
)

// planInt returns 0 for both an absent key and a present-but-negative one, so a
// bare `seconds > 0` read a model-supplied -60 as "unset" and ran the plan
// unbounded with no error. Every other numeric in the budget refuses a
// negative; a timeout the caller asked for and silently did not get is the
// worst version of that inconsistency.
func TestBudgetRejectsNegativeTimeouts(t *testing.T) {
	for field, value := range map[string]float64{
		"max_wall_seconds":  -60,
		"max_stall_seconds": -60,
	} {
		t.Run(field, func(t *testing.T) {
			_, err := ParsePlan(map[string]any{
				"name":   "p",
				"tasks":  []any{map[string]any{"id": "a", "prompt": "x"}},
				"budget": map[string]any{"max_workers": float64(1), field: value},
			}, Limits{MaxTasks: 20, ParentTools: []string{"read_file"}})
			if err == nil {
				t.Fatalf("a negative %s was accepted and silently ignored", field)
			}
			if !strings.Contains(err.Error(), field) {
				t.Errorf("error should name %s, got %v", field, err)
			}
		})
	}
}

// Absent and zero must keep their existing meanings — the fix must not turn
// "unset" into an error.
func TestBudgetStillAcceptsAbsentAndZeroTimeouts(t *testing.T) {
	for name, budget := range map[string]map[string]any{
		"absent": {"max_workers": float64(1)},
		"zero":   {"max_workers": float64(1), "max_wall_seconds": float64(0), "max_stall_seconds": float64(0)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParsePlan(map[string]any{
				"name":   "p",
				"tasks":  []any{map[string]any{"id": "a", "prompt": "x"}},
				"budget": budget,
			}, Limits{MaxTasks: 20, ParentTools: []string{"read_file"}}); err != nil {
				t.Fatalf("%s timeouts should parse: %v", name, err)
			}
		})
	}
}
