package tools

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func semanticTestBudget() outputBudget {
	return outputBudget{maxEstimatedTokens: 140, hardMaxBytes: 900}
}

func TestFileOutputPolicyKeepsCompleteHeadAndTailLines(t *testing.T) {
	lines := []string{"File: huge.go (500 lines)", "1 | package main"}
	for index := 2; index < 500; index++ {
		lines = append(lines, fmt.Sprintf("%d | value := %d", index, index))
	}
	lines = append(lines, "500 | // END_MARK")
	got := budgetSemanticOutput(strings.Join(lines, "\n"), outputCategoryFile, semanticTestBudget())
	if !got.truncated || !strings.Contains(got.text, "File: huge.go") || !strings.Contains(got.text, "END_MARK") {
		t.Fatalf("file policy lost boundaries: %#v", got)
	}
	if !utf8.ValidString(got.text) || strings.Contains(got.text, "value := 49\ufffd") {
		t.Fatalf("file policy produced invalid or split text: %q", got.text)
	}
}

func TestSearchOutputPolicyPreservesMultiFileCoverage(t *testing.T) {
	var lines []string
	for _, file := range []string{"a.go", "b.go", "c.go", "d.go"} {
		for line := 1; line <= 30; line++ {
			lines = append(lines, fmt.Sprintf("%s:%d: match value %d", file, line, line))
		}
	}
	lines = append(lines, "120 matches found")
	got := budgetSemanticOutput(strings.Join(lines, "\n"), outputCategorySearch, semanticTestBudget())
	for _, want := range []string{"a.go:", "b.go:", "c.go:", "d.go:", "120 matches"} {
		if !strings.Contains(got.text, want) {
			t.Fatalf("search policy missing %q:\n%s", want, got.text)
		}
	}
}

func TestSearchOutputPolicyPreservesWindowsPathCoverage(t *testing.T) {
	var lines []string
	for _, file := range []string{"a.go", "b.go", "c.go", "d.go"} {
		for line := 1; line <= 30; line++ {
			lines = append(lines, fmt.Sprintf(`C:\workspace\%s:%d: match value %d`, file, line, line))
		}
	}
	lines = append(lines, "120 matches found")
	got := budgetSemanticOutput(strings.Join(lines, "\n"), outputCategorySearch, semanticTestBudget())
	for _, want := range []string{`C:\workspace\a.go:`, `C:\workspace\b.go:`, `C:\workspace\c.go:`, `C:\workspace\d.go:`, "120 matches"} {
		if !strings.Contains(got.text, want) {
			t.Fatalf("search policy missing %q:\n%s", want, got.text)
		}
	}
}

func TestSearchResultFileIgnoresNumericMatchContent(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{line: `C:\workspace\a.go:12: error code:123: failed`, want: `C:\workspace\a.go`},
		{line: `/workspace/a.go:12: error code:123: failed`, want: `/workspace/a.go`},
		{line: `/workspace/name:with-colon.go:12: match`, want: `/workspace/name:with-colon.go`},
	}
	for _, test := range tests {
		if got := searchResultFile(test.line); got != test.want {
			t.Errorf("searchResultFile(%q) = %q, want %q", test.line, got, test.want)
		}
	}
}

func TestProcessOutputPolicyCollapsesRepetitiveLogsAndKeepsDiagnostics(t *testing.T) {
	input := "starting server\n" + strings.Repeat("polling...\n", 300) + "WARNING: queue slow\nERROR: request failed\nshutdown complete"
	got := budgetSemanticOutput(input, outputCategoryProcess, semanticTestBudget())
	for _, want := range []string{"starting server", "repeated", "WARNING", "ERROR", "shutdown complete"} {
		if !strings.Contains(got.text, want) {
			t.Fatalf("process policy missing %q:\n%s", want, got.text)
		}
	}
	if strings.Count(got.text, "polling...") > 1 {
		t.Fatalf("repetitive line was not collapsed:\n%s", got.text)
	}
}

func TestTestOutputPolicyKeepsFailureAndFinalSummary(t *testing.T) {
	input := "=== RUN TestSuite\n" + strings.Repeat("ok progress\n", 300) +
		"--- FAIL: TestImportant (0.01s)\n    thing_test.go:42: expected 7, got 9\nFAIL\nexit status 1"
	got := budgetSemanticOutput(input, outputCategoryTest, semanticTestBudget())
	for _, want := range []string{"TestImportant", "expected 7", "FAIL", "exit status 1"} {
		if !strings.Contains(got.text, want) {
			t.Fatalf("test policy missing %q:\n%s", want, got.text)
		}
	}
}

func TestDiffOutputPolicyKeepsFilesAndCompleteHunks(t *testing.T) {
	var diff strings.Builder
	for file := 1; file <= 4; file++ {
		fmt.Fprintf(&diff, "diff --git a/f%d.go b/f%d.go\n--- a/f%d.go\n+++ b/f%d.go\n", file, file, file, file)
		for hunk := 1; hunk <= 8; hunk++ {
			fmt.Fprintf(&diff, "@@ -%d,2 +%d,2 @@\n-old%d_%d\n+new%d_%d\n", hunk, hunk, file, hunk, file, hunk)
		}
	}
	got := budgetSemanticOutput(diff.String(), outputCategoryDiff, outputBudget{maxEstimatedTokens: 220, hardMaxBytes: 1400})
	for file := 1; file <= 4; file++ {
		if !strings.Contains(got.text, fmt.Sprintf("diff --git a/f%d.go", file)) {
			t.Fatalf("diff policy lost file %d header:\n%s", file, got.text)
		}
	}
	for _, line := range strings.Split(got.text, "\n") {
		if strings.HasPrefix(line, "@@ ") {
			// Each retained hunk is an indivisible three-line unit.
			if !strings.Contains(got.text, line+"\n-old") {
				t.Fatalf("hunk header was sliced from its body: %q", line)
			}
		}
	}
}

func TestWorkerOutputPolicyKeepsStatusErrorsAndConclusion(t *testing.T) {
	input := "session_id: child_1\nstatus: error\n" + strings.Repeat("worker progress\n", 200) +
		"tools executed: grep, read_file\nchanged files: a.go, b.go\nerror: verification failed\nConclusion: fix the nil check before merging."
	got := budgetSemanticOutput(input, outputCategoryWorker, semanticTestBudget())
	for _, want := range []string{"session_id", "status: error", "tools executed", "changed files", "verification failed", "Conclusion"} {
		if !strings.Contains(got.text, want) {
			t.Fatalf("worker policy missing %q:\n%s", want, got.text)
		}
	}
}

func TestSemanticPoliciesAreDeterministic(t *testing.T) {
	input := "start\n" + strings.Repeat("tick\n", 500) + "ERROR: boom\nend"
	want := budgetSemanticOutput(input, outputCategoryProcess, semanticTestBudget())
	for iteration := 0; iteration < 20; iteration++ {
		if got := budgetSemanticOutput(input, outputCategoryProcess, semanticTestBudget()); got != want {
			t.Fatalf("iteration %d differs:\n got %#v\nwant %#v", iteration, got, want)
		}
	}
}
