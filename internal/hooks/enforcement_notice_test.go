package hooks

import (
	"context"
	"strings"
	"testing"
)

// A HOOK THAT RAN UNDER THE WEAKENED TOKEN STILL SAYS SO, ON THE TYPED CHANNEL.
//
// The projection once kept stdout, stderr and an exit code and dropped the
// notices, so such a hook ran silently. The first fix put them into the message
// alongside the hook's own output, which delivered them but made Messages two
// things at once. Now that the agent loop merges Notices into the typed
// EnforcementNotices for beforeTool and afterTool alike, folding them into the
// message as well delivered the same disclosure twice.
//
// So the property is unchanged and its carrier moved: whatever the hook printed,
// the notice reaches Notices exactly once and never rides along in Messages.
func TestAHookSurfacesTheEnforcementNoticeOnTheTypedChannel(t *testing.T) {
	const notice = "denyRead is configured, so the write jail is not confining writes"

	for _, testCase := range []struct {
		name   string
		result commandResult
	}{
		{"hook printed nothing", commandResult{ExitCode: 0, Notices: []string{notice}}},
		{"hook printed to stdout", commandResult{ExitCode: 0, Stdout: "looks fine", Notices: []string{notice}}},
		{"hook printed to stderr only", commandResult{ExitCode: 0, Stderr: "a warning", Notices: []string{notice}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			result := testCase.result
			dispatcher := NewDispatcher(DispatcherOptions{
				Config: beforeToolConfig(Definition{ID: "vet", Event: EventAfterTool, Command: "vet", Enabled: true}),
				run: func(context.Context, string, []string, []byte, string, []string) commandResult {
					return result
				},
			})
			outcome := dispatcher.Dispatch(context.Background(), DispatchInput{Event: EventAfterTool, ToolName: "bash"})

			if got := strings.Count(strings.Join(outcome.Notices, "\n"), notice); got != 1 {
				t.Fatalf("the notice reached Notices %d times, want once: %v", got, outcome.Notices)
			}
			// AND NOT IN THE PROSE AS WELL, which is what made it arrive twice.
			if joined := strings.Join(outcome.Messages, "\n"); strings.Contains(joined, notice) {
				t.Errorf("the notice also rode along in Messages, so every surface that composes the typed slice shows it twice:\n%s", joined)
			}
		})
	}
}

// A hook's own output is untouched, with a notice or without one.
func TestAHookMessageCarriesOnlyTheHooksOwnOutput(t *testing.T) {
	const notice = "denyRead is configured, so the write jail is not confining writes"
	if message := hookMessage(commandResult{ExitCode: 0, Stdout: "looks fine"}); message != "looks fine" {
		t.Errorf("hookMessage = %q, want the hook's own output untouched", message)
	}
	if message := hookMessage(commandResult{ExitCode: 0, Stdout: "looks fine", Notices: []string{notice}}); message != "looks fine" {
		t.Errorf("hookMessage = %q, want the notice left to the typed channel", message)
	}
	if message := hookMessage(commandResult{ExitCode: 0}); message != "" {
		t.Errorf("a silent hook with no notice produced %q", message)
	}
	if message := hookMessage(commandResult{ExitCode: 0, Notices: []string{notice}}); message != "" {
		t.Errorf("a silent hook produced %q, want nothing: its disclosure travels typed", message)
	}
}

// THROUGH Dispatch, NOT A HAND-BUILT commandResult.
//
// The blocking branch builds DispatchOutcome.Reason with blockReason and returns
// immediately, so it never touches hookMessage. A vetoing beforeTool hook that
// ran without write confinement reported only the veto, and Reason is the field
// the agent turns into the model-visible result.
func TestABlockedBeforeToolHookCarriesTheNoticeIntoItsReason(t *testing.T) {
	const notice = "denyRead is configured, so the write jail is not confining writes"

	dispatcher := NewDispatcher(DispatcherOptions{
		Config: beforeToolConfig(Definition{ID: "policy", Event: EventBeforeTool, Command: "policy-check", Enabled: true}),
		run: func(context.Context, string, []string, []byte, string, []string) commandResult {
			return commandResult{ExitCode: 2, Stderr: "policy violation", Notices: []string{notice}}
		},
	})

	outcome := dispatcher.Dispatch(context.Background(), DispatchInput{Event: EventBeforeTool, ToolName: "bash"})
	if !outcome.Blocked {
		t.Fatal("SETUP INVALID: the hook did not block, so the blocking branch was never taken")
	}
	if !strings.Contains(outcome.Reason, notice) {
		t.Errorf("the veto reason lost the enforcement notice:\n%s", outcome.Reason)
	}
	if !strings.Contains(outcome.Reason, "policy violation") {
		t.Errorf("the veto reason lost the hook's own explanation:\n%s", outcome.Reason)
	}
	if strings.Count(outcome.Reason, notice) != 1 {
		t.Errorf("the notice appears %d times in the reason, want once:\n%s", strings.Count(outcome.Reason, notice), outcome.Reason)
	}
}

// And a veto with no notice reads exactly as it did before.
func TestABlockedHookWithoutANoticeIsUnchanged(t *testing.T) {
	dispatcher := NewDispatcher(DispatcherOptions{
		Config: beforeToolConfig(Definition{ID: "policy", Event: EventBeforeTool, Command: "policy-check", Enabled: true}),
		run: func(context.Context, string, []string, []byte, string, []string) commandResult {
			return commandResult{ExitCode: 2, Stderr: "policy violation"}
		},
	})
	outcome := dispatcher.Dispatch(context.Background(), DispatchInput{Event: EventBeforeTool, ToolName: "bash"})
	if outcome.Reason != "policy violation" {
		t.Errorf("Reason = %q, want the hook's own explanation untouched", outcome.Reason)
	}
}
