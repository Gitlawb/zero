package hooks

import (
	"context"
	"strings"
	"testing"
)

// Same contract on the hook path. The projection kept stdout, stderr and an exit
// code and dropped the enforcement notices, so a hook ran under the weakened
// token silently.
func TestAHookSurfacesTheEnforcementNotice(t *testing.T) {
	const notice = "denyRead is configured, so the write jail is not confining writes"

	for _, testCase := range []struct {
		name   string
		result commandResult
		want   string
	}{
		{"hook printed nothing", commandResult{ExitCode: 0, Notices: []string{notice}}, notice},
		{"hook printed to stdout", commandResult{ExitCode: 0, Stdout: "looks fine", Notices: []string{notice}}, notice},
		{"hook printed to stderr only", commandResult{ExitCode: 0, Stderr: "a warning", Notices: []string{notice}}, notice},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			message := hookMessage(testCase.result)
			if !strings.Contains(message, testCase.want) {
				t.Fatalf("the hook message does not carry the notice:\n%s", message)
			}
			if strings.Count(message, testCase.want) != 1 {
				t.Errorf("the notice appears %d times, want exactly once:\n%s", strings.Count(message, testCase.want), message)
			}
		})
	}
}

// A hook with no notice reads exactly as it did before.
func TestAHookWithoutANoticeIsUnchanged(t *testing.T) {
	if message := hookMessage(commandResult{ExitCode: 0, Stdout: "looks fine"}); message != "looks fine" {
		t.Errorf("hookMessage = %q, want the hook's own output untouched", message)
	}
	if message := hookMessage(commandResult{ExitCode: 0}); message != "" {
		t.Errorf("a silent hook with no notice produced %q", message)
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
