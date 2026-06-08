package agent

import (
	"context"
	"testing"

	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// TestOptionsModelSwitcherFieldExists asserts the escalation hook is an
// assignable field on Options with the agreed signature, and that nil is its
// zero value (escalation disabled by default).
func TestOptionsModelSwitcherFieldExists(t *testing.T) {
	var options Options
	if options.ModelSwitcher != nil {
		t.Fatalf("expected ModelSwitcher to default to nil, got non-nil")
	}
	options.ModelSwitcher = func(_ context.Context, modelID string) (Provider, error) {
		return &mockProvider{}, nil
	}
	provider, err := options.ModelSwitcher(context.Background(), "claude-sonnet-4.5")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := provider.(*mockProvider); !ok {
		t.Fatalf("expected ModelSwitcher to return a Provider, got %T", provider)
	}
}

// TestToolResultRequestedModelFieldExists asserts the loop-level escalation
// signal field is present on ToolResult and empty for a normal result.
func TestToolResultRequestedModelFieldExists(t *testing.T) {
	var result ToolResult
	if result.RequestedModel != "" {
		t.Fatalf("expected RequestedModel to default to empty, got %q", result.RequestedModel)
	}
	result.RequestedModel = "claude-opus-4.1"
	if result.RequestedModel != "claude-opus-4.1" {
		t.Fatalf("expected RequestedModel to round-trip, got %q", result.RequestedModel)
	}
	// Keep the zeroruntime import load-bearing so the file compiles standalone.
	_ = zeroruntime.MessageRoleAssistant
}

// escalatingTool is a registered fake tool that returns the escalation signal
// in result Meta (mirroring the real escalate_model tool's contract), used to
// drive the loop-level switch in tests without depending on the tools package.
type escalatingTool struct {
	target string
}

func (t escalatingTool) Name() string       { return "escalate" }
func (t escalatingTool) Description() string { return "requests a model switch for testing" }
func (t escalatingTool) Parameters() tools.Schema {
	return tools.Schema{Type: "object", AdditionalProperties: false}
}
func (t escalatingTool) Safety() tools.Safety {
	return tools.Safety{SideEffect: tools.SideEffectRead, Permission: tools.PermissionAllow}
}
func (t escalatingTool) Run(_ context.Context, _ map[string]any) tools.Result {
	meta := map[string]string{}
	if t.target != "" {
		meta["escalate_to_model"] = t.target
	}
	return tools.Result{Status: tools.StatusOK, Output: "escalating", Meta: meta}
}

// TestExecuteToolCallLiftsEscalationMeta verifies executeToolCall promotes the
// tool's Meta["escalate_to_model"] into ToolResult.RequestedModel.
func TestExecuteToolCallLiftsEscalationMeta(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(escalatingTool{target: "claude-opus-4.1"})

	result, abortErr := executeToolCall(
		context.Background(),
		registry,
		ToolCall{ID: "c1", Name: "escalate"},
		PermissionModeAuto,
		Options{},
	)
	if abortErr != nil {
		t.Fatal(abortErr)
	}
	if result.RequestedModel != "claude-opus-4.1" {
		t.Fatalf("expected RequestedModel lifted from meta, got %q", result.RequestedModel)
	}
}

// TestExecuteToolCallNoEscalationMetaLeavesRequestedModelEmpty verifies a normal
// tool result (no escalate_to_model meta) leaves RequestedModel empty.
func TestExecuteToolCallNoEscalationMetaLeavesRequestedModelEmpty(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(escalatingTool{target: ""})

	result, abortErr := executeToolCall(
		context.Background(),
		registry,
		ToolCall{ID: "c1", Name: "escalate"},
		PermissionModeAuto,
		Options{},
	)
	if abortErr != nil {
		t.Fatal(abortErr)
	}
	if result.RequestedModel != "" {
		t.Fatalf("expected empty RequestedModel without escalation meta, got %q", result.RequestedModel)
	}
}
