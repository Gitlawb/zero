package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/tools"
)

// A shell call that sets sandbox_permissions: with_additional_permissions but
// omits (or malforms) additional_permissions can never succeed: the same
// validation runs again at grant time regardless of the user's decision. It
// must be rejected as a plain tool error BEFORE any permission prompt, not
// surfaced as a confusing "approved but denied" prompt outcome.
func TestExecuteToolCallRejectsMissingAdditionalPermissionsBeforePrompt(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	registry.Register(tools.NewBashTool(root))

	promptCalls := 0
	result, err := executeToolCall(
		context.Background(),
		registry,
		ToolCall{
			ID:        "c1",
			Name:      "bash",
			Arguments: `{"command":"echo hi","sandbox_permissions":"with_additional_permissions"}`,
		},
		PermissionModeAuto,
		Options{
			OnPermissionRequest: func(ctx context.Context, request PermissionRequest) (PermissionDecision, error) {
				promptCalls++
				return PermissionDecision{Action: PermissionDecisionAllow}, nil
			},
		},
	)
	if err != nil {
		t.Fatalf("executeToolCall: %v", err)
	}
	if result.Status != tools.StatusError {
		t.Fatalf("status = %q, want error", result.Status)
	}
	if promptCalls != 0 {
		t.Fatalf("OnPermissionRequest called %d times, want 0 (must reject before prompting)", promptCalls)
	}
}

// The mismatched-flag case (additional_permissions present without the
// sandbox_permissions flag) must be rejected the same way.
func TestExecuteToolCallRejectsAdditionalPermissionsWithoutFlagBeforePrompt(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	registry.Register(tools.NewBashTool(root))

	promptCalls := 0
	result, err := executeToolCall(
		context.Background(),
		registry,
		ToolCall{
			ID:        "c1",
			Name:      "bash",
			Arguments: `{"command":"echo hi","additional_permissions":{"network":{"enabled":true}}}`,
		},
		PermissionModeAuto,
		Options{
			OnPermissionRequest: func(ctx context.Context, request PermissionRequest) (PermissionDecision, error) {
				promptCalls++
				return PermissionDecision{Action: PermissionDecisionAllow}, nil
			},
		},
	)
	if err != nil {
		t.Fatalf("executeToolCall: %v", err)
	}
	if result.Status != tools.StatusError {
		t.Fatalf("status = %q, want error", result.Status)
	}
	if promptCalls != 0 {
		t.Fatalf("OnPermissionRequest called %d times, want 0 (must reject before prompting)", promptCalls)
	}
}

// The rejection message must show a concrete, correctly-shaped example: a
// model that gets this wrong once (attaching sandbox_permissions out of habit
// from an earlier call, without a valid payload) needs enough in the error to
// self-correct on retry instead of repeating the identical mistake.
func TestExecuteToolCallMissingAdditionalPermissionsErrorIsActionable(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	registry.Register(tools.NewBashTool(root))

	result, err := executeToolCall(
		context.Background(),
		registry,
		ToolCall{
			ID:        "c1",
			Name:      "bash",
			Arguments: `{"command":"ping example.com","sandbox_permissions":"with_additional_permissions"}`,
		},
		PermissionModeAuto,
		Options{},
	)
	if err != nil {
		t.Fatalf("executeToolCall: %v", err)
	}
	if !strings.Contains(result.Output, `{"network": {"enabled": true}}`) {
		t.Fatalf("output = %q, want a concrete additional_permissions example", result.Output)
	}
	if !strings.Contains(result.Output, "omit sandbox_permissions") {
		t.Fatalf("output = %q, want guidance that the flag can simply be omitted", result.Output)
	}
}

// A well-formed additional_permissions payload must still reach the normal
// permission prompt: this fix only rejects malformed payloads.
func TestExecuteToolCallStillPromptsForValidAdditionalPermissions(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	registry.Register(tools.NewBashTool(root))

	promptCalls := 0
	_, err := executeToolCall(
		context.Background(),
		registry,
		ToolCall{
			ID:   "c1",
			Name: "bash",
			Arguments: `{"command":"echo hi","sandbox_permissions":"with_additional_permissions",` +
				`"additional_permissions":{"network":{"enabled":true}}}`,
		},
		PermissionModeAuto,
		Options{
			OnPermissionRequest: func(ctx context.Context, request PermissionRequest) (PermissionDecision, error) {
				promptCalls++
				return PermissionDecision{Action: PermissionDecisionDeny}, nil
			},
		},
	)
	if err != nil {
		t.Fatalf("executeToolCall: %v", err)
	}
	if promptCalls != 1 {
		t.Fatalf("OnPermissionRequest called %d times, want exactly 1 for a valid elevation request", promptCalls)
	}
}
