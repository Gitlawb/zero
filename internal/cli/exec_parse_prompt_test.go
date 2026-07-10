package cli

import (
	"strings"
	"testing"
)

// TestParseExecArgsInlinePromptKeepsLeadingDash guards the M19 fix: the top-level
// `zero -p "<prompt>"` path forwards the prompt as the inline `--prompt=<value>`
// form precisely so a prompt whose first character is a dash is taken verbatim
// instead of being mistaken for a flag and rejected.
func TestParseExecArgsInlinePromptKeepsLeadingDash(t *testing.T) {
	opts, early, err := parseExecArgs([]string{"--prompt=-foo bar"})
	if err != nil {
		t.Fatalf("inline dash-leading prompt rejected: %v", err)
	}
	if early {
		t.Fatal("a normal prompt parse must not request early exit")
	}
	if got := strings.TrimSpace(strings.Join(opts.promptParts, " ")); got != "-foo bar" {
		t.Fatalf("inline prompt = %q, want %q", got, "-foo bar")
	}
}

func TestParseExecArgsPreventsPositionalFromBeingConsumedAsFlagValue(t *testing.T) {
	_, _, err := parseExecArgs([]string{"--auto", "do a build"})
	if err == nil || !strings.Contains(err.Error(), "Invalid autonomy level") {
		t.Fatalf("expected error that --auto has invalid autonomy level, got: %v", err)
	}

	_, _, err = parseExecArgs([]string{"--notify", "do a build"})
	if err == nil || !strings.Contains(err.Error(), "invalid --notify") {
		t.Fatalf("expected error that --notify is invalid, got: %v", err)
	}

	_, _, err = parseExecArgs([]string{"--max-turns", "do a build"})
	if err == nil || !strings.Contains(err.Error(), "invalid --max-turns") {
		t.Fatalf("expected error that --max-turns is invalid, got: %v", err)
	}
}
