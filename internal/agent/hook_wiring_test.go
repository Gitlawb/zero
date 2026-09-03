package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/hooks"
	"github.com/Gitlawb/zero/internal/tools"
)

func TestAppendHookFeedbackFormatsOutput(t *testing.T) {
	// Blank feedback leaves the original output untouched and reports no redaction.
	if got, redacted := appendHookFeedback("tool output", "   "); got != "tool output" || redacted {
		t.Fatalf("blank feedback should not change output or redact, got %q redacted=%v", got, redacted)
	}
	// Feedback is appended under a header alongside the existing output.
	got, redacted := appendHookFeedback("tool output", "gofmt reformatted main.go")
	if !strings.Contains(got, "tool output") || !strings.Contains(got, "Hook output:") || !strings.Contains(got, "gofmt reformatted main.go") {
		t.Fatalf("expected combined tool + hook output, got %q", got)
	}
	if redacted {
		t.Fatal("clean feedback should not be reported as redacted")
	}
	// With no original output the hook feedback stands alone under the header.
	if got, _ := appendHookFeedback("", "validation ran"); !strings.HasPrefix(got, "Hook output:") || !strings.Contains(got, "validation ran") {
		t.Fatalf("expected standalone hook output, got %q", got)
	}
}

func TestAppendHookFeedbackScrubsSecretsAndFlagsRedaction(t *testing.T) {
	// A hook that echoes a secret must be scrubbed before it reaches the model,
	// and the redaction must be reported so the caller can flag ToolResult.Redacted.
	got, redacted := appendHookFeedback("tool output", "leaked token ghp_abcdefghijklmnopqrstuvwxyz0123456789")
	if !redacted {
		t.Fatal("expected redaction to be reported for secret-bearing feedback")
	}
	if strings.Contains(got, "ghp_abcdefghijklmnopqrstuvwxyz0123456789") {
		t.Fatalf("secret leaked into hook feedback: %q", got)
	}
}

func TestBlockedByHookResultCarriesReasonAndDenial(t *testing.T) {
	out := blockedByHookResult(
		ToolCall{ID: "c1", Name: "write_file"},
		hooks.DispatchOutcome{Blocked: true, BlockedBy: "policy", Reason: "writes under /etc are denied"},
	)
	if out.Status != tools.StatusError {
		t.Fatalf("status = %v, want error", out.Status)
	}
	if out.DenialReason != DenialHookBlocked {
		t.Fatalf("denial = %q, want %q", out.DenialReason, DenialHookBlocked)
	}
	if out.ToolCallID != "c1" || out.Name != "write_file" {
		t.Fatalf("call identity not propagated: %#v", out)
	}
	for _, want := range []string{"write_file", "policy", "writes under /etc are denied"} {
		if !strings.Contains(out.Output, want) {
			t.Fatalf("output %q missing %q", out.Output, want)
		}
	}
	if out.Redacted {
		t.Fatal("a clean hook reason must not flag Redacted")
	}
}

func TestBlockedByHookResultScrubsSecretReason(t *testing.T) {
	out := blockedByHookResult(
		ToolCall{ID: "c3", Name: "bash"},
		hooks.DispatchOutcome{Blocked: true, BlockedBy: "secret-scan", Reason: "denied: found ghp_abcdefghijklmnopqrstuvwxyz0123456789 in args"},
	)
	if !out.Redacted {
		t.Fatal("a scrubbed hook reason must set ToolResult.Redacted")
	}
	if strings.Contains(out.Output, "ghp_abcdefghijklmnopqrstuvwxyz0123456789") {
		t.Fatalf("secret leaked into blocked-hook output: %q", out.Output)
	}
}

func TestBlockedByHookResultFallsBackWhenReasonEmpty(t *testing.T) {
	out := blockedByHookResult(ToolCall{ID: "c2", Name: "bash"}, hooks.DispatchOutcome{Blocked: true, BlockedBy: "x"})
	if !strings.Contains(out.Output, "blocked by a beforeTool hook") {
		t.Fatalf("expected a default reason, got %q", out.Output)
	}
}

func TestDispatchHelpersAreNoopWithoutDispatcher(t *testing.T) {
	options := Options{} // Hooks is nil
	if _, blocked := dispatchBeforeTool(context.Background(), options, ToolCall{Name: "bash"}, nil); blocked {
		t.Fatal("a nil dispatcher must never block a tool")
	}
	if feedback := dispatchAfterTool(context.Background(), options, ToolCall{Name: "bash"}, nil, tools.Result{}); feedback != "" {
		t.Fatalf("a nil dispatcher must yield no feedback, got %q", feedback)
	}
}

// A SUCCESSFUL beforeTool HOOK'S OUTPUT MUST REACH THE MODEL, NOT ONLY THE AUDIT.
//
// executeToolCall used to read the beforeTool outcome only when Blocked was true,
// so a hook that ran fine and produced an enforcement notice — for instance that
// it ran under the weakened DenyRead token — put that notice in the audit record
// and nowhere anybody could see it. Only vetoes and afterTool feedback reached a
// surface. joinHookMessages is the delivery: beforeTool's messages ride out on
// the same tool result afterTool feedback already uses.
func TestJoinHookMessagesDeliversSuccessfulBeforeToolOutput(t *testing.T) {
	const notice = "hook ran without WRITE_RESTRICTED because denyRead is configured"

	// A successful beforeTool hook alone still reaches the model.
	if got := joinHookMessages([]string{notice}, ""); got != notice {
		t.Fatalf("a successful beforeTool notice was dropped: %q", got)
	}
	// And it does not displace afterTool feedback; both arrive, in order.
	got := joinHookMessages([]string{notice}, "gofmt reformatted main.go")
	if !strings.Contains(got, notice) || !strings.Contains(got, "gofmt reformatted main.go") {
		t.Fatalf("expected both the beforeTool notice and the afterTool feedback, got %q", got)
	}
	if strings.Index(got, notice) > strings.Index(got, "gofmt reformatted main.go") {
		t.Fatalf("beforeTool output should precede afterTool feedback, got %q", got)
	}
	// Empty and whitespace-only messages contribute nothing, so a run with no hook
	// output stays silent rather than appending an empty header.
	if got := joinHookMessages([]string{"", "   "}, ""); got != "" {
		t.Fatalf("blank hook messages produced %q, want nothing", got)
	}
	// afterTool alone is unchanged, which is the behaviour that already worked.
	if got := joinHookMessages(nil, "vet found an issue"); got != "vet found an issue" {
		t.Fatalf("afterTool-only feedback changed shape: %q", got)
	}
}
