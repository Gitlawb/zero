package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/sandbox"
	"github.com/Gitlawb/zero/internal/tools"
)

type normalizationRecordingShellTool struct {
	calls []map[string]any
}

func (tool *normalizationRecordingShellTool) Name() string { return "bash" }
func (tool *normalizationRecordingShellTool) Description() string {
	return "record normalized shell arguments"
}
func (tool *normalizationRecordingShellTool) Parameters() tools.Schema {
	return tools.Schema{Type: "object", AdditionalProperties: true}
}
func (tool *normalizationRecordingShellTool) Safety() tools.Safety {
	return tools.Safety{SideEffect: tools.SideEffectShell, Permission: tools.PermissionAllow}
}
func (tool *normalizationRecordingShellTool) Run(_ context.Context, args map[string]any) tools.Result {
	tool.calls = append(tool.calls, cloneArgs(args))
	return tools.Result{Status: tools.StatusOK}
}

// A shell call that sets sandbox_permissions: with_additional_permissions but
// omits (or malforms) additional_permissions can never succeed: the same
// validation runs again at grant time regardless of the user's decision. It
// must be rejected as a plain tool error BEFORE any permission prompt, not
// surfaced as a confusing "approved but denied" prompt outcome.
func TestExecuteToolCallRejectsMissingAdditionalPermissionsBeforePrompt(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	registry.Register(tools.NewScopedBashTool(root, nil))

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
	registry.Register(tools.NewScopedBashTool(root, nil))

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
	registry.Register(tools.NewScopedBashTool(root, nil))

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
	registry.Register(tools.NewScopedBashTool(root, nil))

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

func TestNormalizeProviderExpandedEmptyAdditionalPermissions(t *testing.T) {
	for _, mode := range []string{
		string(tools.SandboxPermissionsUseDefault),
		string(tools.SandboxPermissionsWithAdditionalPermissions),
	} {
		args := map[string]any{
			"command":             "./zero --version",
			"sandbox_permissions": mode,
			"additional_permissions": map[string]any{
				"file_system": map[string]any{
					"deny_read": []any{},
					"entries":   []any{},
					"read":      []any{},
					"write":     []any{},
				},
				"network": map[string]any{"enabled": false},
			},
		}
		normalizeNoopAdditionalPermissions(args, t.TempDir(), nil)
		if _, exists := args["additional_permissions"]; exists {
			t.Fatalf("mode %q retained empty additional_permissions: %#v", mode, args)
		}
		if got := args["sandbox_permissions"]; got != string(tools.SandboxPermissionsUseDefault) {
			t.Fatalf("mode %q normalized to %q, want use_default", mode, got)
		}
	}
}

func TestExecuteToolCallNormalizesProviderExpandedEmptyAdditionalPermissions(t *testing.T) {
	tool := &normalizationRecordingShellTool{}
	registry := tools.NewRegistry()
	registry.Register(tool)

	result, err := executeToolCall(
		context.Background(),
		registry,
		ToolCall{
			ID:   "c1",
			Name: "bash",
			Arguments: `{"command":"./zero --version","sandbox_permissions":"with_additional_permissions",` +
				`"additional_permissions":{"file_system":{"deny_read":[],"entries":[],"read":[],"write":[]},"network":{"enabled":false}}}`,
		},
		PermissionModeAuto,
		Options{Cwd: t.TempDir()},
	)
	if err != nil {
		t.Fatalf("executeToolCall: %v", err)
	}
	if result.Status != tools.StatusOK {
		t.Fatalf("provider-expanded empty permissions were rejected: %s", result.Output)
	}
	if len(tool.calls) != 1 {
		t.Fatalf("shell tool calls = %d, want 1", len(tool.calls))
	}
	args := tool.calls[0]
	if _, exists := args["additional_permissions"]; exists {
		t.Fatalf("executeToolCall retained empty additional_permissions: %#v", args)
	}
	if got := args["sandbox_permissions"]; got != string(tools.SandboxPermissionsUseDefault) {
		t.Fatalf("sandbox mode = %q, want use_default", got)
	}
}

func TestNormalizeAdditionalPermissionsPreservesRealRequest(t *testing.T) {
	args := map[string]any{
		"sandbox_permissions": string(tools.SandboxPermissionsWithAdditionalPermissions),
		"additional_permissions": map[string]any{
			"network": map[string]any{"enabled": true},
		},
	}
	normalizeNoopAdditionalPermissions(args, t.TempDir(), nil)
	if _, exists := args["additional_permissions"]; !exists {
		t.Fatal("non-empty additional_permissions was removed")
	}
	if got := args["sandbox_permissions"]; got != string(tools.SandboxPermissionsWithAdditionalPermissions) {
		t.Fatalf("sandbox mode changed to %q", got)
	}
}

func TestNormalizeAdditionalPermissionsRemovesWorkspaceReadAlreadyCoveredBySandbox(t *testing.T) {
	root := t.TempDir()
	engine := sandbox.NewEngine(sandbox.EngineOptions{
		WorkspaceRoot: root,
		Policy:        sandbox.DefaultPolicy(),
	})
	args := map[string]any{
		"command":             "./zero --version",
		"sandbox_permissions": string(tools.SandboxPermissionsWithAdditionalPermissions),
		"additional_permissions": map[string]any{
			"file_system": map[string]any{
				"entries": []any{
					map[string]any{
						"access": "read",
						"path": map[string]any{
							"type": "path",
							"path": ".",
						},
					},
				},
				"deny_read": []any{},
				"read":      []any{},
				"write":     []any{},
			},
			"network": map[string]any{"enabled": false},
		},
	}

	normalizeNoopAdditionalPermissions(args, root, engine)

	if _, exists := args["additional_permissions"]; exists {
		t.Fatalf("already-covered additional_permissions was retained: %#v", args)
	}
	if got := args["sandbox_permissions"]; got != string(tools.SandboxPermissionsUseDefault) {
		t.Fatalf("sandbox mode changed to %q", got)
	}
}
