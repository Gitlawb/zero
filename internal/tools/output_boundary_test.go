package tools

import (
	"context"
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
