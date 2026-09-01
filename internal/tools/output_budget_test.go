package tools

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestEstimateOutputTokensASCIIAndUnicode(t *testing.T) {
	if got := estimateOutputTokens("abcd efgh"); got != 2 {
		t.Fatalf("ASCII estimate = %d, want 2", got)
	}
	unicodeText := "🙂界"
	if got := estimateOutputTokens(unicodeText); got < len([]byte(unicodeText)) {
		t.Fatalf("Unicode estimate = %d, want conservative estimate >= %d", got, len([]byte(unicodeText)))
	}
	if first, second := estimateOutputTokens(unicodeText), estimateOutputTokens(unicodeText); first != second {
		t.Fatalf("estimator is not deterministic: %d != %d", first, second)
	}
	if input := strings.Repeat("\u0080", 4); estimateOutputTokens(input) < len([]byte(input)) {
		t.Fatalf("U+0080 estimate = %d, want conservative estimate >= %d", estimateOutputTokens(input), len([]byte(input)))
	}
}

func TestBudgetDefaultOutputLeavesSmallOutputByteIdentical(t *testing.T) {
	input := "small output\nwith spacing\n"
	got := budgetDefaultOutput(input, outputBudget{maxEstimatedTokens: 100, hardMaxBytes: 1024})
	if got.text != input {
		t.Fatalf("small output changed: got %q want %q", got.text, input)
	}
	if got.truncated || got.originalBytes != len(input) || got.retainedBytes != len(input) {
		t.Fatalf("unexpected small-output metadata: %#v", got)
	}
}

func TestBudgetDefaultOutputKeepsUTF8HeadTailWithinHardCeiling(t *testing.T) {
	input := "HEAD🙂\n" + strings.Repeat("界", 200) + "\nTAIL🙂"
	const hardMax = 120
	got := budgetDefaultOutput(input, outputBudget{maxEstimatedTokens: 10_000, hardMaxBytes: hardMax})
	if !got.truncated {
		t.Fatal("large output was not truncated")
	}
	if len(got.text) > hardMax {
		t.Fatalf("retained %d bytes, hard ceiling %d", len(got.text), hardMax)
	}
	if !utf8.ValidString(got.text) {
		t.Fatalf("budgeted output is invalid UTF-8: %q", got.text)
	}
	for _, want := range []string{"HEAD", "TAIL", "output truncated"} {
		if !strings.Contains(got.text, want) {
			t.Fatalf("budgeted output missing %q: %q", want, got.text)
		}
	}
}

func TestBudgetDefaultOutputHonorsEstimatedTokenBudget(t *testing.T) {
	input := strings.Repeat("🙂", 100)
	got := budgetDefaultOutput(input, outputBudget{maxEstimatedTokens: 60, hardMaxBytes: 1024})
	if !got.truncated || got.reason != "estimated_token_budget" {
		t.Fatalf("unexpected token-budget result: %#v", got)
	}
	if got.estimatedRetainedTokens > 60 {
		t.Fatalf("retained estimate = %d, budget 60", got.estimatedRetainedTokens)
	}
}

func TestBudgetDefaultOutputDeterministic(t *testing.T) {
	input := "head\n" + strings.Repeat("same line\n", 1000) + "tail\n"
	budget := outputBudget{maxEstimatedTokens: 80, hardMaxBytes: 512}
	want := budgetDefaultOutput(input, budget)
	for iteration := 0; iteration < 20; iteration++ {
		got := budgetDefaultOutput(input, budget)
		if got != want {
			t.Fatalf("iteration %d differs:\n got %#v\nwant %#v", iteration, got, want)
		}
	}
}

func TestBudgetDefaultOutputTinyBudgetsNeverExceedTheirCeilings(t *testing.T) {
	input := strings.Repeat("oversized output ", 100)
	for _, budget := range []outputBudget{
		{hardMaxBytes: len(defaultSemanticTruncationNotice) - 1},
		{maxEstimatedTokens: estimateOutputTokens(defaultSemanticTruncationNotice) - 1},
	} {
		got := budgetDefaultOutput(input, budget)
		if !got.truncated || !fitsOutputBudget(got.text, budget) {
			t.Fatalf("tiny budget result does not fit: budget=%#v result=%#v", budget, got)
		}
	}
}
