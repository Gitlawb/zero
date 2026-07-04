package tools

import (
	"strings"
	"testing"
)

func TestFormatAskUserAnswersDistinguishesSkipFromBlank(t *testing.T) {
	questions := []AskUserQuestion{
		{Question: "Which database?"},
		{Question: "Migrate existing data?"},
	}

	// Wholesale dismissal (nothing answered): flagged as a skip up front.
	dismissed := FormatAskUserAnswers(questions, []string{"", ""})
	if !strings.Contains(dismissed, "dismissed this prompt") || !strings.Contains(dismissed, "Treat this as a skip") {
		t.Fatalf("all-empty answers must be flagged as a skip, got:\n%s", dismissed)
	}
	if strings.Contains(dismissed, "left blank") {
		t.Fatalf("a full dismissal should read as skipped, not left-blank:\n%s", dismissed)
	}
	if !strings.Contains(dismissed, "(skipped)") {
		t.Fatalf("dismissed questions should render (skipped):\n%s", dismissed)
	}

	// Partial answer: the blank one is "left blank", no wholesale-skip note.
	partial := FormatAskUserAnswers(questions, []string{"postgres", ""})
	if strings.Contains(partial, "dismissed this prompt") {
		t.Fatalf("a partial answer is not a dismissal:\n%s", partial)
	}
	if !strings.Contains(partial, "postgres") || !strings.Contains(partial, "(left blank)") {
		t.Fatalf("partial answers should show the answer and mark the blank one left-blank:\n%s", partial)
	}
}
