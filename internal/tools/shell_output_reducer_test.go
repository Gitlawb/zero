package tools

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestReduceCommandOutputCompactsPassingGoPackagesAndKeepsRawArtifact(t *testing.T) {
	var lines []string
	lines = append(lines, "output:")
	for index := 0; index < commandReducerMinPassingLines+3; index++ {
		lines = append(lines, fmt.Sprintf("ok  \texample.test/pkg%d\t0.01s", index))
	}
	lines = append(lines, "exit_code: 0")
	original := strings.Join(lines, "\n")

	result := reduceCommandOutput(ExecCommandToolName, map[string]any{"cmd": "go test ./..."}, Result{
		Status: StatusOK,
		Output: original,
	})

	if result.Output == original || !strings.Contains(result.Output, "passing package lines omitted") {
		t.Fatalf("expected compact result, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "exit_code: 0") {
		t.Fatalf("exit status was lost: %q", result.Output)
	}
	spillPath := result.Meta["spill_path"]
	spilled, err := os.ReadFile(spillPath)
	if err != nil {
		t.Fatalf("read raw artifact: %v", err)
	}
	if string(spilled) != original {
		t.Fatalf("raw artifact differs from original output")
	}

	budgeted := applySelfManagedOutputBudget(
		NewExecCommandTool(t.TempDir(), newExecSessionManager()),
		ExecCommandToolName,
		map[string]any{"cmd": "go test ./..."},
		result,
	)
	if budgeted.Meta["spill_path"] != spillPath {
		t.Fatalf("budget boundary replaced the exact raw artifact: got %q want %q", budgeted.Meta["spill_path"], spillPath)
	}
	spilled, err = os.ReadFile(budgeted.Meta["spill_path"])
	if err != nil || string(spilled) != original {
		t.Fatalf("budgeted raw artifact was not preserved: err=%v", err)
	}
}

func TestReduceCommandOutputFailsOpenForCompoundAndFailedOutput(t *testing.T) {
	failure := "output:\n--- FAIL: TestThing\npanic: broken\nFAIL\texample.test/pkg\nexit_code: 1"
	for _, command := range []string{"go test ./... | tee test.log", "go test ./... && echo done"} {
		result := reduceCommandOutput(ExecCommandToolName, map[string]any{"cmd": command}, Result{Status: StatusError, Output: failure})
		if result.Output != failure || result.Meta != nil {
			t.Fatalf("compound command %q should pass through, got %#v", command, result)
		}
	}
	result := reduceCommandOutput(ExecCommandToolName, map[string]any{"cmd": "go test ./..."}, Result{Status: StatusError, Output: failure})
	if result.Output != failure {
		t.Fatalf("failure evidence should pass through when there is no repetitive success bulk: %q", result.Output)
	}
}

func TestSelfManagedBudgetPreservesRawSpillWhenReducedOutputAlsoTruncates(t *testing.T) {
	raw := "raw output\n" + strings.Repeat("ok  \texample.test/raw\t0.01s\n", 1000)
	rawSpill := spillTruncatedOutput(ExecCommandToolName, raw)
	if rawSpill == "" {
		t.Fatal("failed to create raw spill")
	}
	reduced := strings.Repeat("reduced but still large\n", 1000)
	result := applySelfManagedOutputBudget(
		NewExecCommandTool(t.TempDir(), newExecSessionManager()),
		ExecCommandToolName,
		map[string]any{"cmd": "go test ./...", "max_output_tokens": 100},
		Result{
			Status:    StatusOK,
			Output:    reduced,
			Truncated: true,
			Meta: map[string]string{
				"spill_path":        rawSpill,
				"truncation_reason": "command_aware_reduction",
			},
		},
	)

	if result.Meta["spill_path"] != rawSpill {
		t.Fatalf("budget boundary replaced raw spill: got %q want %q", result.Meta["spill_path"], rawSpill)
	}
	spilled, err := os.ReadFile(result.Meta["spill_path"])
	if err != nil || string(spilled) != raw {
		t.Fatalf("raw spill was not preserved: err=%v", err)
	}
}
