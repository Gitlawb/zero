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

func TestSelfBudgetedToolDeclaresSemanticCategoryWithoutRebudgeting(t *testing.T) {
	registry := NewRegistry()
	registry.Register(NewBashTool(t.TempDir()))
	tool, _ := registry.Get("bash")
	result := Result{Status: StatusOK, Output: "already bounded", Meta: map[string]string{}}
	got := annotateSelfBudgetedOutput(tool, "bash", map[string]any{"command": "go test ./..."}, result)
	if got.Output != result.Output || got.Meta[outputBudgetCategoryMeta] != string(outputCategoryTest) {
		t.Fatalf("self-budgeted category annotation changed output or lost category: %#v", got)
	}
}
