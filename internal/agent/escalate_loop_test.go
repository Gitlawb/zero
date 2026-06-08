package agent

import (
	"context"
	"testing"

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
