package tools

import (
	"context"
	"os"
	"strings"
	"testing"
)

type outcomeErrorTool struct {
	ceilingFakeTool
	display Display
}

func (tool outcomeErrorTool) Run(context.Context, map[string]any) Result {
	return Result{Status: StatusError, Output: tool.output, Display: tool.display}
}

func TestRegistryFinalizesSeparateModelHumanAndArtifactViews(t *testing.T) {
	setTestTempDir(t)
	lines := []string{"output:"}
	for index := 0; index < 24; index++ {
		lines = append(lines, "ok  \texample.test/package\t0.01s")
	}
	lines = append(lines, "exit_code: 0")
	raw := strings.Join(lines, "\n")

	registry := NewRegistry()
	registry.Register(newCeilingFakeTool(ExecCommandToolName, raw))
	result := registry.Run(context.Background(), ExecCommandToolName, map[string]any{"cmd": "go test ./..."})

	if !result.Outcome.Finalized() {
		t.Fatal("registry result did not finalize a tool outcome")
	}
	if result.ModelOutput() != result.Output || result.Outcome.ModelView != result.Output {
		t.Fatalf("model representations drifted: output=%q outcome=%q", result.Output, result.Outcome.ModelView)
	}
	if strings.Count(result.ModelOutput(), "ok  \t") != 0 {
		t.Fatalf("model view retained repetitive passing-package lines: %q", result.ModelOutput())
	}
	if !strings.Contains(result.HumanDisplay().Preview, "ok  \texample.test/package") {
		t.Fatalf("human preview lost the raw passing-package evidence: %q", result.HumanDisplay().Preview)
	}
	if result.Outcome.Artifact == nil || !result.Outcome.Artifact.CompleteAtBoundary {
		t.Fatalf("missing complete boundary artifact: %#v", result.Outcome.Artifact)
	}
	artifact, err := os.ReadFile(result.Outcome.Artifact.Path)
	if err != nil {
		t.Fatalf("read outcome artifact: %v", err)
	}
	if string(artifact) != raw {
		t.Fatal("outcome artifact differs from the exact boundary output")
	}
	diagnostics := result.Outcome.Diagnostics
	if diagnostics.OriginalBytes != len(raw) || diagnostics.ModelBytes != len(result.Output) {
		t.Fatalf("incorrect outcome byte diagnostics: %#v", diagnostics)
	}
	if diagnostics.EstimatedModelTokens >= diagnostics.EstimatedOriginalTokens {
		t.Fatalf("expected reduced model view: %#v", diagnostics)
	}
}

func TestRegistryOutcomeRedactsModelHumanAndArtifactViews(t *testing.T) {
	setTestTempDir(t)
	secret := "ghp_" + strings.Repeat("s", 36)
	raw := "output:\n" + secret + "\n" + strings.Repeat("ok  \texample.test/package\t0.01s\n", 24) + "exit_code: 0"

	registry := NewRegistry()
	registry.Register(newCeilingFakeTool(ExecCommandToolName, raw))
	result := registry.Run(context.Background(), ExecCommandToolName, map[string]any{"cmd": "go test ./..."})

	if !result.Outcome.Diagnostics.Redacted {
		t.Fatal("outcome did not record redaction")
	}
	for name, value := range map[string]string{
		"model": result.ModelOutput(),
		"human": result.HumanDisplay().Preview,
	} {
		if strings.Contains(value, secret) {
			t.Fatalf("%s view leaked secret", name)
		}
	}
	artifact, err := os.ReadFile(result.Outcome.Artifact.Path)
	if err != nil {
		t.Fatalf("read outcome artifact: %v", err)
	}
	if strings.Contains(string(artifact), secret) {
		t.Fatal("artifact leaked secret")
	}
}

func TestDirectToolResultUsesLegacyViewFallbacks(t *testing.T) {
	result := Result{
		Output:  "direct output",
		Display: Display{Summary: "direct summary"},
	}
	if result.Outcome.Finalized() {
		t.Fatal("direct result unexpectedly finalized")
	}
	if result.ModelOutput() != result.Output || result.HumanDisplay() != result.Display {
		t.Fatalf("direct result fallbacks changed: %#v", result)
	}
}

func TestRebudgetAfterHookPreservesExecutionArtifactAndRefreshesModelView(t *testing.T) {
	setTestTempDir(t)
	raw := "output:\n" + strings.Repeat("ok  \texample.test/package\t0.01s\n", 24) + "exit_code: 0"
	registry := NewRegistry()
	registry.Register(newCeilingFakeTool(ExecCommandToolName, raw))
	result := registry.Run(context.Background(), ExecCommandToolName, map[string]any{"cmd": "go test ./..."})
	artifact := result.Outcome.Artifact
	originalBytes := result.Outcome.Diagnostics.OriginalBytes

	result.Output += "\nafter-hook diagnostic"
	result = registry.RebudgetAfterHook(ExecCommandToolName, map[string]any{"cmd": "go test ./..."}, result)

	if result.Outcome.Artifact != artifact {
		t.Fatalf("rebudget replaced the execution artifact: before=%#v after=%#v", artifact, result.Outcome.Artifact)
	}
	if result.Outcome.Diagnostics.OriginalBytes != originalBytes {
		t.Fatalf("rebudget lost original execution size: before=%d after=%d", originalBytes, result.Outcome.Diagnostics.OriginalBytes)
	}
	if !strings.Contains(result.ModelOutput(), "after-hook diagnostic") {
		t.Fatalf("rebudget did not refresh model view: %q", result.ModelOutput())
	}
}

func TestToolOutcomeCorpusMetrics(t *testing.T) {
	setTestTempDir(t)
	totalOriginalTokens := 0
	totalModelTokens := 0
	for _, testCase := range commandReducerCorpus() {
		t.Run(testCase.name, func(t *testing.T) {
			registry := NewRegistry()
			registry.Register(newCeilingFakeTool(ExecCommandToolName, testCase.output))
			result := registry.Run(context.Background(), ExecCommandToolName, map[string]any{"cmd": testCase.command})

			if !result.Outcome.Finalized() || result.Outcome.Artifact == nil {
				t.Fatalf("corpus result lacks a finalized recoverable outcome: %#v", result.Outcome)
			}
			if result.HumanDisplay().Preview != testCase.output {
				t.Fatal("bounded corpus output was not preserved exactly for the human view")
			}
			diagnostics := result.Outcome.Diagnostics
			totalOriginalTokens += diagnostics.EstimatedOriginalTokens
			totalModelTokens += diagnostics.EstimatedModelTokens
			t.Logf("original_tokens=%d model_tokens=%d human_bytes=%d artifact_complete=%t",
				diagnostics.EstimatedOriginalTokens,
				diagnostics.EstimatedModelTokens,
				len(result.HumanDisplay().Preview),
				result.Outcome.Artifact.CompleteAtBoundary,
			)
		})
	}
	if totalModelTokens >= totalOriginalTokens {
		t.Fatalf("outcome corpus did not reduce model context: original=%d model=%d", totalOriginalTokens, totalModelTokens)
	}
	t.Logf("aggregate original_tokens=%d model_tokens=%d reduction_pct=%d",
		totalOriginalTokens,
		totalModelTokens,
		100*(totalOriginalTokens-totalModelTokens)/totalOriginalTokens,
	)
}

func TestErrorOutcomeUsesRawCommandEvidenceButDropsUnrelatedPreview(t *testing.T) {
	setTestTempDir(t)
	raw := "output:\n" + strings.Repeat("ok  \texample.test/package\t0.01s\n", 24) +
		"--- FAIL: TestImportant\nexpected 7, got 9\nFAIL\nexit_code: 1"

	registry := NewRegistry()
	registry.Register(outcomeErrorTool{ceilingFakeTool: newCeilingFakeTool(ExecCommandToolName, raw)})
	result := registry.Run(context.Background(), ExecCommandToolName, map[string]any{"cmd": "go test ./..."})
	if !strings.Contains(result.HumanDisplay().Preview, "TestImportant") ||
		!strings.Contains(result.HumanDisplay().Preview, "ok  \texample.test/package") {
		t.Fatalf("error outcome lost raw command evidence: %q", result.HumanDisplay().Preview)
	}

	registry = NewRegistry()
	registry.Register(outcomeErrorTool{
		ceilingFakeTool: newCeilingFakeTool("write_failure", "Error: permission denied"),
		display:         Display{Preview: "prospective diff must not hide error"},
	})
	result = registry.Run(context.Background(), "write_failure", map[string]any{})
	if result.HumanDisplay().Preview != "" {
		t.Fatalf("ordinary error retained an unrelated preview: %q", result.HumanDisplay().Preview)
	}
}
