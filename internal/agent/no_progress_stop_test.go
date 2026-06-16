package agent

import "testing"

func TestIsNoProgressStopRequiresFullStructure(t *testing.T) {
	// The actual guardrail answer (any turn count) is recognized.
	for _, turns := range []int{1, 3, 12} {
		if !IsNoProgressStop(noOutputStopAnswer(turns)) {
			t.Fatalf("the real no-output stop answer (turns=%d) should be recognized", turns)
		}
	}

	// A legitimate message that merely QUOTES the marker must NOT be classified as
	// a no-progress stop — that would wrongly hide a real session from /resume.
	rejected := []string{
		`The previous run ended "with no output (no visible text and no tool calls)" so here is what I did instead.`,
		"with no output (no visible text and no tool calls)", // bare marker, no prefix/suffix
		"Agent stopped after 3 turns.",                       // prefix only
		"to avoid consuming tokens without making progress.", // suffix only
		"Here is your e-commerce site.",                      // unrelated
		"",
		// Hostile input: prefix + marker + suffix ALL present, but the marker is
		// quoted mid-sentence rather than being the guard's own structured answer.
		// A loose prefix/contains/suffix check misclassifies this.
		`Agent stopped after 3 turns. The marker is "with no output (no visible text and no tool calls)" so here it is: to avoid consuming tokens without making progress.`,
		// Arbitrary text wedged between the marker and the suffix.
		"Agent stopped after 3 turns with no output (no visible text and no tool calls) and then some more text to avoid consuming tokens without making progress.",
		// Non-integer "turn count" between the prefix and the marker.
		"Agent stopped after several turns with no output (no visible text and no tool calls) to avoid consuming tokens without making progress.",
	}
	for _, content := range rejected {
		if IsNoProgressStop(content) {
			t.Fatalf("content must not be classified as a no-progress stop: %q", content)
		}
	}
}
