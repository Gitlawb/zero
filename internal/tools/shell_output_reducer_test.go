package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

type commandReducerFixtureTool struct {
	name   string
	output string
}

func (tool commandReducerFixtureTool) Name() string { return tool.name }
func (tool commandReducerFixtureTool) Description() string {
	return "returns command output for reducer integration tests"
}
func (tool commandReducerFixtureTool) Parameters() Schema {
	return Schema{Type: "object", AdditionalProperties: true}
}
func (tool commandReducerFixtureTool) Safety() Safety {
	return Safety{SideEffect: SideEffectRead, Permission: PermissionAllow, Reason: "test output"}
}
func (tool commandReducerFixtureTool) Run(context.Context, map[string]any) Result {
	return Result{Status: StatusOK, Output: tool.output}
}

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

func TestReduceGoTestPassingPackagesPreservesCoverageResults(t *testing.T) {
	output := strings.Join([]string{
		"ok  \texample.test/a\t0.01s\tcoverage: 82.4% of statements",
		"?   \texample.test/b\t[no test files]",
		"ok  \texample.test/c\t0.01s",
	}, "\n")

	reduced, omitted := reduceGoTestPassingPackages(output)
	if !strings.Contains(reduced, "coverage: 82.4% of statements") {
		t.Fatalf("coverage result was removed: %q", reduced)
	}
	if !strings.Contains(reduced, "[no test files]") {
		t.Fatalf("no-test-files result was removed: %q", reduced)
	}
	if omitted != 1 {
		t.Fatalf("omitted=%d, want only the plain passing line omitted", omitted)
	}
}

func TestReduceGoTestPassingPackagesPreservesIndentedTestLogs(t *testing.T) {
	output := strings.Join([]string{
		"    ok  user-provided test log",
		"    ?   another test log",
		"ok  \texample.test/pkg\t0.01s",
	}, "\n")

	reduced, omitted := reduceGoTestPassingPackages(output)
	if !strings.Contains(reduced, "    ok  user-provided test log") ||
		!strings.Contains(reduced, "    ?   another test log") {
		t.Fatalf("indented test log was removed: %q", reduced)
	}
	if omitted != 1 {
		t.Fatalf("omitted=%d, want only the package result omitted", omitted)
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

func TestReduceCommandOutputCompactsRecognizedTestAndBuildNoise(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	for _, testCase := range commandReducerCorpus()[1:] {
		t.Run(testCase.name, func(t *testing.T) {
			result := reduceCommandOutput(ExecCommandToolName, map[string]any{"cmd": testCase.command}, Result{
				Status: StatusOK,
				Output: testCase.output,
			})
			if result.Meta["command_output_reduced"] != "true" {
				t.Fatalf("recognized command output was not reduced: %#v", result)
			}
			if !strings.Contains(result.Output, testCase.want) || !strings.Contains(result.Output, "exit_code: 0") {
				t.Fatalf("decisive summary or exit status was lost: %q", result.Output)
			}
			spillPath := result.Meta["spill_path"]
			spilled, err := os.ReadFile(spillPath)
			if err != nil {
				t.Fatalf("read raw artifact: %v", err)
			}
			if string(spilled) != testCase.output {
				t.Fatal("raw artifact differs from original output")
			}
		})
	}
}

func TestReduceCommandOutputPreservesFailureEvidence(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	passing := make([]string, 0, commandReducerMinPassingLines+2)
	for index := 0; index < commandReducerMinPassingLines+2; index++ {
		passing = append(passing, fmt.Sprintf("test module::case_%02d ... ok", index))
	}
	original := strings.Join(append(passing,
		"test module::broken ... FAILED",
		"failures:",
		"---- module::broken stdout ----",
		"assertion failed: expected zero",
		"test result: FAILED. 14 passed; 1 failed; finished in 0.08s",
		"exit_code: 101",
	), "\n")

	result := reduceCommandOutput(ExecCommandToolName, map[string]any{"cmd": "cargo test --workspace"}, Result{
		Status: StatusError,
		Output: original,
	})
	for _, evidence := range []string{"module::broken ... FAILED", "assertion failed: expected zero", "14 passed; 1 failed", "exit_code: 101"} {
		if !strings.Contains(result.Output, evidence) {
			t.Fatalf("failure evidence %q was lost: %q", evidence, result.Output)
		}
	}
	if result.Meta["command_output_reduced"] != "true" {
		t.Fatalf("repetitive passing output was not reduced: %#v", result)
	}
}

func TestReduceCommandOutputPassesThroughUnsupportedAndCompoundCommands(t *testing.T) {
	original := strings.Repeat("PASS src/example.test.ts\n", commandReducerMinPassingLines+2) + "exit_code: 0"
	for _, command := range []string{
		"npm test | tee test.log",
		"cargo test && echo done",
		"pytest; notify-send done",
		"cargo test 1>test.log",
		"cargo test 2>>errors.log",
		"git status --short",
	} {
		result := reduceCommandOutput(ExecCommandToolName, map[string]any{"cmd": command}, Result{Status: StatusOK, Output: original})
		if result.Output != original || result.Meta != nil {
			t.Fatalf("command %q should pass through exactly, got %#v", command, result)
		}
	}
}

func TestReduceCommandOutputAllowsTrailingStderrMerge(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	fixture := commandReducerCorpus()[1]
	result := reduceCommandOutput(ExecCommandToolName, map[string]any{"cmd": fixture.command + " 2>&1"}, Result{
		Status: StatusOK,
		Output: fixture.output,
	})
	if result.Meta["command_output_reducer"] != "cargo_test" {
		t.Fatalf("trailing stderr merge prevented reduction: %#v", result)
	}
}

func TestReduceCommandOutputRequiresAuthoritativeRunnerSummary(t *testing.T) {
	lookalikes := map[string]string{
		"cargo test":  "test user::message ... ok",
		"cargo check": "Checking a custom deployment state",
		"pytest":      "application log ........ [100%]",
		"npm test":    "PASS user-provided log message",
	}
	for command, line := range lookalikes {
		original := strings.Repeat(line+"\n", commandReducerMinPassingLines+2) + "exit_code: 0"
		result := reduceCommandOutput(ExecCommandToolName, map[string]any{"cmd": command}, Result{Status: StatusOK, Output: original})
		if result.Output != original || result.Meta != nil {
			t.Fatalf("summary-free output for %q should pass through, got %#v", command, result)
		}
	}
}

func TestRegistryAppliesCommandReducerToExecAndBashTools(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	fixture := commandReducerCorpus()[1]
	for _, toolName := range []string{ExecCommandToolName, "bash"} {
		t.Run(toolName, func(t *testing.T) {
			registry := NewRegistry()
			registry.Register(commandReducerFixtureTool{name: toolName, output: fixture.output})
			args := map[string]any{"cmd": fixture.command}
			if toolName == "bash" {
				args = map[string]any{"command": fixture.command}
			}
			result := registry.Run(context.Background(), toolName, args)
			if result.Status != StatusOK || result.Meta["command_output_reducer"] != "cargo_test" {
				t.Fatalf("registry result was not reduced: %#v", result)
			}
			if !strings.Contains(result.Output, fixture.want) {
				t.Fatalf("registry reduction lost summary: %q", result.Output)
			}
			spilled, err := os.ReadFile(result.Meta["spill_path"])
			if err != nil || string(spilled) != fixture.output {
				t.Fatalf("registry raw artifact mismatch: err=%v", err)
			}
		})
	}
}
