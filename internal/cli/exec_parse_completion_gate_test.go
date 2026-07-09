package cli

import "testing"

// --no-completion-gate exists for conversational exec callers (a chat frontend
// with an operator present): an honest blocker report that hands the turn back
// must not be downgraded to INCOMPLETE there. The flag maps to
// agent.Options.RequireCompletionSignal = false; default keeps the gate on.
func TestParseExecArgsNoCompletionGate(t *testing.T) {
	options, _, err := parseExecArgs([]string{"--prompt", "hi", "--no-completion-gate"})
	if err != nil {
		t.Fatalf("parseExecArgs: %v", err)
	}
	if !options.noCompletionGate {
		t.Fatal("--no-completion-gate must set noCompletionGate")
	}

	options, _, err = parseExecArgs([]string{"--prompt", "hi"})
	if err != nil {
		t.Fatalf("parseExecArgs: %v", err)
	}
	if options.noCompletionGate {
		t.Fatal("noCompletionGate must default to false (gate on)")
	}
}
