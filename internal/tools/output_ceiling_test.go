package tools

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
)

// ceilingFakeTool is a minimal tool with no output budget of its own — the
// stand-in for an MCP-served tool or any future builtin that forgets to cap
// its output.
type ceilingFakeTool struct {
	baseTool
	output string
}

func (tool ceilingFakeTool) Run(context.Context, map[string]any) Result {
	return okResult(tool.output)
}

// ceilingExemptTool is the same fake but marked self-budgeting.
type ceilingExemptTool struct{ ceilingFakeTool }

func (ceilingExemptTool) managesOutputBudget() {}

func newCeilingFakeTool(name, output string) ceilingFakeTool {
	return ceilingFakeTool{
		baseTool: baseTool{name: name, safety: readOnlySafety("test tool")},
		output:   output,
	}
}

// An unbudgeted tool's oversized output is capped at the universal ceiling,
// head and tail survive, and the full output is spilled to a re-readable file.
func TestOutputCeilingCapsUnbudgetedTool(t *testing.T) {
	setTestTempDir(t)
	big := "HEAD_MARK\n" + strings.Repeat("z", defaultOutputCeilingTokens*4*3) + "\nTAIL_MARK"

	registry := NewRegistry()
	registry.Register(newCeilingFakeTool("fake_big", big))
	result := registry.Run(context.Background(), "fake_big", map[string]any{})

	if !result.Truncated {
		t.Fatal("over-ceiling output must report truncated=true")
	}
	maxBytes := defaultOutputCeilingTokens * 4
	if len(result.Output) > maxBytes+512 {
		t.Fatalf("emitted %d bytes, ceiling is %d", len(result.Output), maxBytes)
	}
	if !strings.Contains(result.Output, "HEAD_MARK") || !strings.Contains(result.Output, "TAIL_MARK") {
		t.Fatal("head/tail lost at the ceiling")
	}
	if result.Meta["raw_bytes"] != strconv.Itoa(len(big)) {
		t.Fatalf("raw_bytes = %s, want %d", result.Meta["raw_bytes"], len(big))
	}
	if !strings.Contains(result.Output, "full output received by budgeting layer saved to ") {
		t.Fatalf("ceiling truncation must include a spill hint: %q", result.Output[:200])
	}
	start := strings.Index(result.Output, "full output received by budgeting layer saved to ") + len("full output received by budgeting layer saved to ")
	end := strings.Index(result.Output[start:], " (grep")
	content, err := os.ReadFile(result.Output[start : start+end])
	if err != nil {
		t.Fatalf("spill file unreadable: %v", err)
	}
	if string(content) != big {
		t.Fatalf("spill must hold the full output: got %d bytes, want %d", len(content), len(big))
	}
}

// A self-budgeting tool's output passes the boundary untouched even when it
// exceeds the universal ceiling — its own (possibly model-raised) budget wins.
func TestOutputCeilingSkipsSelfBudgetingTool(t *testing.T) {
	big := strings.Repeat("z", defaultOutputCeilingTokens*4*2)
	registry := NewRegistry()
	registry.Register(ceilingExemptTool{newCeilingFakeTool("fake_exempt", big)})

	result := registry.Run(context.Background(), "fake_exempt", map[string]any{})
	if result.Truncated || result.Output != big {
		t.Fatalf("self-budgeting tool must bypass the ceiling (truncated=%v, %d bytes)", result.Truncated, len(result.Output))
	}
}

// Under-ceiling output is untouched and gains no budget metadata.
func TestOutputCeilingSmallOutputUntouched(t *testing.T) {
	registry := NewRegistry()
	registry.Register(newCeilingFakeTool("fake_small", "hello"))

	result := registry.Run(context.Background(), "fake_small", map[string]any{})
	if result.Truncated || result.Output != "hello" {
		t.Fatalf("small output altered: %+v", result)
	}
	if _, ok := result.Meta["raw_bytes"]; ok {
		t.Fatal("small output must not gain budget metadata")
	}
}

// ZERO_TOOL_OUTPUT_CEILING_TOKENS tightens, loosens, or disables the ceiling.
func TestOutputCeilingEnvOverride(t *testing.T) {
	setTestTempDir(t)
	big := strings.Repeat("z", 100*1024)

	t.Setenv(outputCeilingEnv, "1000")
	registry := NewRegistry()
	registry.Register(newCeilingFakeTool("fake_env", big))
	result := registry.Run(context.Background(), "fake_env", map[string]any{})
	if !result.Truncated || len(result.Output) > 1000*4+512 {
		t.Fatalf("ceiling override ignored: truncated=%v, %d bytes", result.Truncated, len(result.Output))
	}

	t.Setenv(outputCeilingEnv, "0")
	result = registry.Run(context.Background(), "fake_env", map[string]any{})
	if result.Truncated || len(result.Output) != len(big) {
		t.Fatalf("ceiling=0 must disable the net: truncated=%v, %d bytes", result.Truncated, len(result.Output))
	}
}

// The exemption list matches the tools that really do budget themselves, and
// the unbudgeted ones (web_fetch, skill, browser) stay under the net.
func TestSelfBudgetingExemptionList(t *testing.T) {
	dir := t.TempDir()
	exempt := []Tool{
		NewBashTool(dir),
		NewExecCommandTool(dir, newExecSessionManager()),
	}
	for _, tool := range exempt {
		if _, ok := tool.(selfBudgeting); !ok {
			t.Errorf("%s must be exempt from the output ceiling", tool.Name())
		}
	}
	if _, ok := NewWebFetchTool().(selfBudgeting); ok {
		t.Error("web_fetch must NOT be exempt — the ceiling is its only budget")
	}
	for _, tool := range []Tool{NewReadFileTool(dir), NewReadMinifiedFileTool(dir), NewGrepTool(dir), NewGlobTool(dir), NewListDirectoryTool(dir)} {
		if _, ok := tool.(selfBudgeting); ok {
			t.Errorf("%s must use the shared post-redaction budget boundary", tool.Name())
		}
	}
}
