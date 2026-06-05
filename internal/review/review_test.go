package review

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeOutcome(t *testing.T) {
	tests := map[string]Outcome{
		"success":         OutcomeSuccess,
		"timed-out":       OutcomeTimedOut,
		"ACTION_REQUIRED": OutcomeActionRequired,
		"":                OutcomeUnknown,
		"nonsense":        OutcomeUnknown,
	}

	for value, want := range tests {
		if got := NormalizeOutcome(value); got != want {
			t.Fatalf("NormalizeOutcome(%q) = %q, want %q", value, got, want)
		}
	}
}

func TestBuildChecksFromEnvDetectsBlockers(t *testing.T) {
	checks := BuildChecksFromEnv(map[string]string{
		"ZERO_REVIEW_DIFF_CHECK": "success",
		"ZERO_REVIEW_TYPECHECK":  "success",
		"ZERO_REVIEW_TEST":       "failure",
		"ZERO_REVIEW_BUILD":      "success",
		"ZERO_REVIEW_SMOKE":      "skipped",
	})

	got := make([]Outcome, 0, len(checks))
	for _, check := range checks {
		got = append(got, check.Outcome)
	}
	want := []Outcome{
		OutcomeSuccess,
		OutcomeSuccess,
		OutcomeFailure,
		OutcomeSuccess,
		OutcomeSkipped,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("outcomes = %#v, want %#v", got, want)
	}
	if !HasBlockingChecks(checks) {
		t.Fatal("expected failed check to be blocking")
	}
}

func TestBuildMarkdownApprovingReview(t *testing.T) {
	checks := BuildChecksFromEnv(map[string]string{
		"ZERO_REVIEW_DIFF_CHECK": "success",
		"ZERO_REVIEW_TYPECHECK":  "success",
		"ZERO_REVIEW_TEST":       "success",
		"ZERO_REVIEW_BUILD":      "success",
		"ZERO_REVIEW_SMOKE":      "success",
	})
	markdown := BuildMarkdown(SummaryInput{
		Number:       30,
		HeadSHA:      "abcdef1234567890",
		Checks:       checks,
		ChangedFiles: []string{"cmd/zero-pr-review/main.go", "internal/review/review_test.go"},
	})

	for _, want := range []string{
		Marker,
		"Verdict: **No blockers found**",
		"- None found.",
		"Head: `abcdef123456`",
		"`internal/review/review_test.go`",
	} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("expected markdown to contain %q:\n%s", want, markdown)
		}
	}
}

func TestBuildMarkdownFormatsFailuresAsBlockers(t *testing.T) {
	checks := BuildChecksFromEnv(map[string]string{
		"ZERO_REVIEW_DIFF_CHECK": "success",
		"ZERO_REVIEW_TYPECHECK":  "failure",
		"ZERO_REVIEW_TEST":       "success",
		"ZERO_REVIEW_BUILD":      "success",
		"ZERO_REVIEW_SMOKE":      "success",
	})
	markdown := BuildMarkdown(SummaryInput{
		Number: 31,
		Checks: checks,
	})

	if !strings.Contains(markdown, "Verdict: **Changes requested**") {
		t.Fatalf("expected changes requested verdict:\n%s", markdown)
	}
	if !strings.Contains(markdown, "`bun run typecheck` ended with `failure`") {
		t.Fatalf("expected typecheck blocker:\n%s", markdown)
	}
	if FormatOutcome(OutcomeSuccess) != "[pass]" {
		t.Fatalf("success outcome formatted as %q", FormatOutcome(OutcomeSuccess))
	}
	if FormatOutcome(OutcomeFailure) != "[fail]" {
		t.Fatalf("failure outcome formatted as %q", FormatOutcome(OutcomeFailure))
	}
}

func TestParseChangedFilesSortsStableList(t *testing.T) {
	got := ParseChangedFiles("tests/b.test.ts\n\nsrc/a.ts\r\n")
	want := []string{"src/a.ts", "tests/b.test.ts"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("files = %#v, want %#v", got, want)
	}
}

func TestBuildMarkdownCapsChangedFiles(t *testing.T) {
	files := make([]string, 0, 14)
	for index := 0; index < 14; index++ {
		files = append(files, fmt.Sprintf("file-%02d.go", index))
	}
	markdown := BuildMarkdown(SummaryInput{
		Checks:       []Check{},
		ChangedFiles: files,
	})

	if !strings.Contains(markdown, "Changed files (14):") {
		t.Fatalf("expected changed file count:\n%s", markdown)
	}
	if !strings.Contains(markdown, "and 2 more") {
		t.Fatalf("expected changed file suffix:\n%s", markdown)
	}
	if strings.Contains(markdown, "`file-13.go`") {
		t.Fatalf("expected file list to be capped:\n%s", markdown)
	}
}

func TestBuildSummaryInputFromEnv(t *testing.T) {
	input := BuildSummaryInputFromEnv(map[string]string{
		"ZERO_PR_NUMBER":         "42",
		"ZERO_REVIEW_HEAD_SHA":   "0123456789abcdef",
		"ZERO_REVIEW_DIFF_CHECK": "success",
		"ZERO_CHANGED_FILES":     "b.go\na.go",
	})

	if input.Number != 42 {
		t.Fatalf("Number = %d, want 42", input.Number)
	}
	if input.HeadSHA != "0123456789abcdef" {
		t.Fatalf("HeadSHA = %q, want supplied SHA", input.HeadSHA)
	}
	if got, want := input.ChangedFiles, []string{"a.go", "b.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ChangedFiles = %#v, want %#v", got, want)
	}
	if input.Checks[0].Outcome != OutcomeSuccess {
		t.Fatalf("first check = %#v, want success", input.Checks[0])
	}
}
