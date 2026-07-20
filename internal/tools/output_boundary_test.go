package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRegistryBudgetRunsAfterRedactionAndSpillsRedactedOutput(t *testing.T) {
	setTestTempDir(t)
	secret := "ghp_" + strings.Repeat("a", 36)
	big := "HEAD\n" + secret + "\n" + strings.Repeat("🙂 noisy output\n", 20_000) + "TAIL"
	registry := NewRegistry()
	registry.Register(newCeilingFakeTool("redacted_big", big))

	result := registry.Run(context.Background(), "redacted_big", map[string]any{})
	if !result.Truncated || result.Meta[outputBudgetSpillCreatedMeta] != "true" {
		t.Fatalf("unexpected budget result: truncated=%t meta=%#v", result.Truncated, result.Meta)
	}
	if strings.Contains(result.Output, secret) || !utf8.ValidString(result.Output) {
		t.Fatalf("exposed output leaked secret or invalid UTF-8: %q", result.Output)
	}
	spillPath := result.Meta["spill_path"]
	content, err := os.ReadFile(spillPath)
	if err != nil {
		t.Fatalf("read spill: %v", err)
	}
	if strings.Contains(string(content), secret) {
		t.Fatal("spill contains unredacted secret")
	}
	if !strings.Contains(string(content), "HEAD") || !strings.Contains(string(content), "TAIL") {
		t.Fatal("spill does not contain the complete redacted output received by the budget layer")
	}
}

func TestRegistryBudgetSpillFailureFallsBackToBoundedOutput(t *testing.T) {
	temp := t.TempDir()
	blockedTemp := filepath.Join(temp, "not-a-directory")
	if err := os.WriteFile(blockedTemp, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", blockedTemp)
	t.Setenv("TMP", blockedTemp)
	t.Setenv("TEMP", blockedTemp)
	t.Setenv(outputCeilingEnv, "100")

	registry := NewRegistry()
	registry.Register(newCeilingFakeTool("spill_failure", strings.Repeat("large output\n", 1000)))
	result := registry.Run(context.Background(), "spill_failure", map[string]any{})
	if !result.Truncated || len(result.Output) > 400 {
		t.Fatalf("fallback is not bounded: truncated=%t bytes=%d", result.Truncated, len(result.Output))
	}
	if result.Meta[outputBudgetSpillCreatedMeta] != "false" || result.Meta["spill_path"] != "" {
		t.Fatalf("spill failure incorrectly advertised: %#v", result.Meta)
	}
}

func TestRegistryMissingPolicyUsesDefaultAndExactHardCeiling(t *testing.T) {
	setTestTempDir(t)
	t.Setenv(outputCeilingEnv, "80")
	input := "HEAD\n" + strings.Repeat("x", 3000) + "\nTAIL"
	registry := NewRegistry()
	registry.Register(newCeilingFakeTool("no_policy", input))
	result := registry.Run(context.Background(), "no_policy", map[string]any{})
	if got := result.Meta[outputBudgetCategoryMeta]; got != string(outputCategoryDefault) {
		t.Fatalf("category = %q, want default", got)
	}
	if len(result.Output) > 80*4 {
		t.Fatalf("output = %d bytes, hard ceiling %d", len(result.Output), 80*4)
	}
	if result.Meta[outputBudgetOriginalBytesMeta] != strconv.Itoa(len(input)) {
		t.Fatalf("original size metadata = %q", result.Meta[outputBudgetOriginalBytesMeta])
	}
}

func TestRegistrySmallOutputRemainsByteIdentical(t *testing.T) {
	registry := NewRegistry()
	registry.Register(newCeilingFakeTool("small_identity", "hello\nworld\n"))
	result := registry.Run(context.Background(), "small_identity", map[string]any{})
	if result.Output != "hello\nworld\n" || result.Truncated {
		t.Fatalf("small output changed: %#v", result)
	}
}

func TestDirectReadAndSearchToolsKeepLegacyByteBudgets(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "large-read.txt"), []byte(strings.Repeat("source line with enough content\n", 7000)), 0o600); err != nil {
		t.Fatal(err)
	}
	if result := NewReadFileTool(root).Run(context.Background(), map[string]any{"path": "large-read.txt"}); !result.Truncated || len(result.Output) > readOutputBudgetBytes {
		t.Fatalf("direct read_file output is not bounded: %#v", result)
	}

	for index := 0; index < 800; index++ {
		name := fmt.Sprintf("%04d-%s.txt", index, strings.Repeat("n", 80))
		if err := os.WriteFile(filepath.Join(root, name), []byte("match\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "large-content.txt"), []byte(strings.Repeat("match "+strings.Repeat("x", 2000)+"\n", 50)), 0o600); err != nil {
		t.Fatal(err)
	}

	for name, result := range map[string]Result{
		"list_directory": NewListDirectoryTool(root).Run(context.Background(), map[string]any{"recursive": false}),
		"glob":           NewGlobTool(root).Run(context.Background(), map[string]any{"pattern": "*.txt", "limit": 1000}),
		"grep files":     NewGrepTool(root).Run(context.Background(), map[string]any{"pattern": "match", "output_mode": "files_with_matches"}),
		"grep content":   NewGrepTool(root).Run(context.Background(), map[string]any{"pattern": "match", "path": "large-content.txt", "head_limit": 50}),
	} {
		if !result.Truncated || len(result.Output) > searchOutputBudgetBytes {
			t.Fatalf("direct %s output is not bounded: truncated=%t bytes=%d meta=%#v", name, result.Truncated, len(result.Output), result.Meta)
		}
	}
}

func TestRegistryLargeFileUsesSemanticFilePolicy(t *testing.T) {
	setTestTempDir(t)
	root := t.TempDir()
	var content strings.Builder
	content.WriteString("HEAD_MARK\n")
	for line := 0; line < 7000; line++ {
		content.WriteString("ordinary source line with enough text to fill the output budget\n")
	}
	content.WriteString("TAIL_MARK\n")
	if err := os.WriteFile(filepath.Join(root, "large.txt"), []byte(content.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	registry.Register(NewReadFileTool(root))
	result := registry.Run(context.Background(), "read_file", map[string]any{"path": "large.txt"})
	if !result.Truncated || result.Meta[outputBudgetCategoryMeta] != string(outputCategoryFile) {
		t.Fatalf("large file was not semantically budgeted: truncated=%t meta=%#v", result.Truncated, result.Meta)
	}
	if len(result.Output) > readOutputBudgetBytes {
		t.Fatalf("large file output = %d bytes, ceiling %d", len(result.Output), readOutputBudgetBytes)
	}
	for _, want := range []string{"File: large.txt", "HEAD_MARK", "TAIL_MARK"} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("large file output missing %q", want)
		}
	}
}

func TestRegistryLargeSingleLineFileRetainsHeadAndTail(t *testing.T) {
	setTestTempDir(t)
	root := t.TempDir()
	content := "HEAD_SINGLE_LINE_MARK" + strings.Repeat("x", readOutputBudgetBytes*2) + "TAIL_SINGLE_LINE_MARK"
	if err := os.WriteFile(filepath.Join(root, "large.min.js"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	registry := NewRegistry()
	registry.Register(NewReadFileTool(root))
	result := registry.Run(context.Background(), "read_file", map[string]any{"path": "large.min.js"})
	if !result.Truncated || result.Meta[outputBudgetCategoryMeta] != string(outputCategoryFile) {
		t.Fatalf("single-line file was not semantically budgeted: truncated=%t meta=%#v", result.Truncated, result.Meta)
	}
	if len(result.Output) > readOutputBudgetBytes || !utf8.ValidString(result.Output) {
		t.Fatalf("single-line file output is not safely bounded: bytes=%d valid=%t", len(result.Output), utf8.ValidString(result.Output))
	}
	if rawBytes, err := strconv.Atoi(result.Meta["raw_bytes"]); err != nil || rawBytes <= len(result.Output) {
		t.Fatalf("single-line file capture was not bounded head/tail output: raw=%q emitted=%d", result.Meta["raw_bytes"], len(result.Output))
	}
	for _, want := range []string{"File: large.min.js", "HEAD_SINGLE_LINE_MARK", "TAIL_SINGLE_LINE_MARK"} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("single-line file output missing %q", want)
		}
	}
}

func TestRegistryMinifiedFileUpdatesEmittedBytesAfterBudgeting(t *testing.T) {
	setTestTempDir(t)
	root := t.TempDir()
	content := "HEAD_MINIFIED_MARK" + strings.Repeat("x", readOutputBudgetBytes*2) + "TAIL_MINIFIED_MARK"
	if err := os.WriteFile(filepath.Join(root, "large.txt"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	registry := NewRegistry()
	registry.Register(NewReadMinifiedFileTool(root))
	result := registry.Run(context.Background(), "read_minified_file", map[string]any{"path": "large.txt"})
	if !result.Truncated || result.Meta["emitted_bytes"] != strconv.Itoa(len(result.Output)) {
		t.Fatalf("minified metadata does not describe final output: truncated=%t emitted=%q bytes=%d", result.Truncated, result.Meta["emitted_bytes"], len(result.Output))
	}
	for _, want := range []string{"HEAD_MINIFIED_MARK", "TAIL_MINIFIED_MARK"} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("minified single-line file output missing %q", want)
		}
	}
}

func TestRegistryGrepUsesSemanticMultiFileCoverage(t *testing.T) {
	setTestTempDir(t)
	root := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.txt", "d.txt"} {
		body := strings.Repeat("needle "+strings.Repeat(name, 15)+"\n", 350)
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	registry := NewRegistry()
	registry.Register(NewGrepTool(root))
	result := registry.Run(context.Background(), "grep", map[string]any{
		"pattern":    "needle",
		"path":       ".",
		"head_limit": 1400,
	})
	if !result.Truncated || result.Meta[outputBudgetCategoryMeta] != string(outputCategorySearch) {
		t.Fatalf("grep was not semantically budgeted: truncated=%t meta=%#v", result.Truncated, result.Meta)
	}
	for _, name := range []string{"a.txt:", "b.txt:", "c.txt:", "d.txt:"} {
		if !strings.Contains(result.Output, name) {
			t.Fatalf("search output lost %s coverage", name)
		}
	}
}

func TestShellOutputCategoryClassification(t *testing.T) {
	tests := []struct {
		command string
		want    outputCategory
	}{
		{"go test ./...", outputCategoryTest},
		{"python -m pytest -q", outputCategoryTest},
		{"git diff --stat main...HEAD", outputCategoryDiff},
		{"git show HEAD", outputCategoryDiff},
		{"make build", outputCategoryProcess},
	}
	for _, test := range tests {
		if got := shellOutputCategory(test.command); got != test.want {
			t.Errorf("shellOutputCategory(%q) = %q, want %q", test.command, got, test.want)
		}
	}
}

func TestShellToolOutputCategoryUsesAllCommandAliases(t *testing.T) {
	providers := []struct {
		name     string
		provider outputPolicyProvider
	}{
		{name: "bash", provider: NewBashTool(t.TempDir()).(outputPolicyProvider)},
		{name: "exec_command", provider: NewExecCommandTool(t.TempDir(), newExecSessionManager()).(outputPolicyProvider)},
	}
	for _, provider := range providers {
		for _, alias := range []string{"command", "cmd", "script", "shell"} {
			t.Run(provider.name+"/"+alias, func(t *testing.T) {
				if got := provider.provider.outputCategory(map[string]any{alias: "go test ./..."}); got != outputCategoryTest {
					t.Fatalf("category = %q, want test", got)
				}
			})
		}
	}

	if got := providers[0].provider.outputCategory(map[string]any{"command": "make build", "cmd": "go test ./..."}); got != outputCategoryProcess {
		t.Fatalf("bash alias precedence category = %q, want process", got)
	}
	if got := providers[1].provider.outputCategory(map[string]any{"cmd": "go test ./...", "command": "make build"}); got != outputCategoryTest {
		t.Fatalf("exec_command alias precedence category = %q, want test", got)
	}
}

func TestRegistryBudgetPreservesExistingTruncationAndRefreshesMetadata(t *testing.T) {
	output := "post-hook output"
	result := applyRegistryOutputBudget(newCeilingFakeTool("existing_truncation", output), "existing_truncation", map[string]any{}, Result{
		Status:    StatusOK,
		Output:    output,
		Truncated: true,
		Meta: map[string]string{
			"emitted_bytes":     "999",
			"estimated_tokens":  "999",
			"truncation_reason": "capture_budget",
		},
	})
	if !result.Truncated || result.Meta["truncated"] != "true" || result.Meta[outputBudgetReasonMeta] != "capture_budget" {
		t.Fatalf("prior truncation state was lost: %#v", result)
	}
	if result.Meta["emitted_bytes"] != strconv.Itoa(len(output)) || result.Meta["estimated_tokens"] != strconv.Itoa(estimateOutputTokens(output)) {
		t.Fatalf("final output metadata is stale: %#v", result.Meta)
	}
	if result.Meta[outputBudgetSpillCreatedMeta] != "false" {
		t.Fatalf("existing truncation unexpectedly created a new spill: %#v", result.Meta)
	}
}

func TestSelfManagedOutputBudgetUsesSemanticTestPolicy(t *testing.T) {
	setTestTempDir(t)
	input := "stdout:\n" + strings.Repeat("PASS progress\n", 8_000) +
		"--- FAIL: TestImportant (0.01s)\nthing_test.go:42: expected 7, got 9\nFAIL\nexit_code: 1"
	tests := []struct {
		name string
		tool Tool
		args map[string]any
	}{
		{name: "bash", tool: NewBashTool(t.TempDir()), args: map[string]any{"command": "go test ./..."}},
		{name: "exec", tool: NewExecCommandTool(t.TempDir(), newExecSessionManager()), args: map[string]any{"cmd": "go test ./...", "max_output_tokens": 16_000}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := applySelfManagedOutputBudget(test.tool, test.tool.Name(), test.args, Result{Status: StatusError, Output: input, Meta: map[string]string{}})
			if !got.Truncated || got.Meta[outputBudgetCategoryMeta] != string(outputCategoryTest) {
				t.Fatalf("semantic budget was not applied: %#v", got)
			}
			for _, want := range []string{"TestImportant", "expected 7", "FAIL", "exit_code: 1"} {
				if !strings.Contains(got.Output, want) {
					t.Fatalf("semantic test output missing %q:\n%s", want, got.Output)
				}
			}
		})
	}
}

type semanticBashFakeTool struct{ ceilingFakeTool }

func (semanticBashFakeTool) managesOutputBudget() {}

func (semanticBashFakeTool) outputCategory(args map[string]any) outputCategory {
	command, _ := args["command"].(string)
	return shellOutputCategory(command)
}

func TestRegistryAppliesSemanticBudgetToSelfManagedShellOutput(t *testing.T) {
	setTestTempDir(t)
	input := "startup\n" + strings.Repeat("PASS progress\n", 8_000) +
		"--- FAIL: TestRegistryImportant (0.01s)\nexpected 7, got 9\nFAIL\nexit_code: 1"
	registry := NewRegistry()
	registry.Register(semanticBashFakeTool{newCeilingFakeTool("bash", input)})
	result := registry.Run(context.Background(), "bash", map[string]any{"command": "go test ./..."})
	if !result.Truncated || result.Meta[outputBudgetReasonMeta] != "semantic_test_budget" {
		t.Fatalf("registry did not apply semantic test budget: %#v", result.Meta)
	}
	for _, want := range []string{"TestRegistryImportant", "expected 7", "FAIL", "exit_code: 1"} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("registry semantic test output missing %q:\n%s", want, result.Output)
		}
	}
}

func TestRegistryAppliesSemanticBudgetToSelfManagedProcessOutput(t *testing.T) {
	setTestTempDir(t)
	input := "starting build\n" + strings.Repeat("progress\n", 8_000) +
		"WARNING: cache is cold\nERROR: compilation failed\nexit_code: 1"
	registry := NewRegistry()
	registry.Register(semanticBashFakeTool{newCeilingFakeTool("bash", input)})
	result := registry.Run(context.Background(), "bash", map[string]any{"command": "make build"})
	if !result.Truncated || result.Meta[outputBudgetReasonMeta] != "semantic_process_budget" {
		t.Fatalf("registry did not apply semantic process budget: %#v", result.Meta)
	}
	for _, want := range []string{"starting build", "WARNING", "ERROR", "exit_code: 1"} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("registry semantic process output missing %q:\n%s", want, result.Output)
		}
	}
}

func TestRebudgetAfterHookPreservesSelfManagedExecBudget(t *testing.T) {
	setTestTempDir(t)
	registry := NewRegistry()
	registry.Register(NewExecCommandTool(t.TempDir(), newExecSessionManager()))

	t.Run("tight call budget remains authoritative", func(t *testing.T) {
		args := map[string]any{"cmd": "make build", "max_output_tokens": 10}
		result := registry.RebudgetAfterHook(ExecCommandToolName, args, Result{Status: StatusOK, Output: strings.Repeat("x", 1_000)})
		if !result.Truncated || len(result.Output) > 40 {
			t.Fatalf("post-hook output escaped call budget: truncated=%t bytes=%d meta=%#v", result.Truncated, len(result.Output), result.Meta)
		}
	})

	t.Run("raised call budget is not replaced by generic ceiling", func(t *testing.T) {
		args := map[string]any{"cmd": "make build", "max_output_tokens": 20_000}
		output := strings.Repeat("x", 70_000)
		result := registry.RebudgetAfterHook(ExecCommandToolName, args, Result{Status: StatusOK, Output: output})
		if result.Truncated || result.Output != output {
			t.Fatalf("post-hook output was incorrectly limited by generic ceiling: truncated=%t bytes=%d", result.Truncated, len(result.Output))
		}
	})
}
