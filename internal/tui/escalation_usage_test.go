package tui

import (
	"context"
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/sessions"
	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// USAGE FOLLOWS THE SWITCH. exec reassigns its currentModel when the escalation
// switcher fires; the TUI captured usageModelID once per run and handed the
// switcher no callback, so every usage event after an escalation was billed to
// the model the run started on. Both attribution paths are pinned here: the
// live per-event message, and the per-event record the final response carries
// for the batch fallback, which must not swing the other way and bill the
// events before the switch to the escalated model.
func TestEscalatedRunAttributesUsageToTheModelInForce(t *testing.T) {
	store := testSessionStore(t)
	session, err := store.Create(sessions.CreateInput{SessionID: "escalation_usage"})
	if err != nil {
		t.Fatal(err)
	}

	// A catalog model with an upgrade target; the target is whatever the
	// catalog says, read back from the provider factory rather than assumed.
	const startModel = "claude-haiku-4.5"
	starting := &scriptedProvider{scripts: [][]zeroruntime.StreamEvent{{
		{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 10, OutputTokens: 1}},
		{Type: zeroruntime.StreamEventToolCallStart, ToolCallID: "escalate", ToolName: "escalate_model"},
		{Type: zeroruntime.StreamEventToolCallDelta, ToolCallID: "escalate", ArgumentsFragment: `{"reason":"harder than it looked"}`},
		{Type: zeroruntime.StreamEventToolCallEnd, ToolCallID: "escalate"},
		{Type: zeroruntime.StreamEventDone},
	}}}
	escalated := &scriptedProvider{scripts: [][]zeroruntime.StreamEvent{{
		{Type: zeroruntime.StreamEventText, Content: "Done on the stronger model."},
		{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 20, OutputTokens: 2}},
		{Type: zeroruntime.StreamEventDone},
	}}}
	registry := tools.NewRegistry()
	registry.Register(tools.NewEscalateModelTool())

	switchedTo := ""
	m := newModel(context.Background(), Options{
		ProviderName:    "anthropic",
		ModelName:       startModel,
		Provider:        starting,
		Registry:        registry,
		SessionStore:    store,
		AllowEscalation: true,
		ProviderProfile: config.ProviderProfile{Model: startModel},
		NewProvider: func(profile config.ProviderProfile) (zeroruntime.Provider, error) {
			switchedTo = profile.Model
			return escalated, nil
		},
	})
	m.activeSession = session
	m.agentOptions.Model = startModel
	var live []string
	m.runtimeMessageSink = func(msg tea.Msg) {
		if usage, ok := msg.(agentUsageMsg); ok {
			live = append(live, usage.modelID)
		}
	}

	msg := execCmd(m.runAgentWithOptions(1, context.Background(), "do the hard thing", nil, tuiAgentRunOptions{}))
	response, ok := msg.(agentResponseMsg)
	if !ok {
		t.Fatalf("run returned %T, want agentResponseMsg", msg)
	}
	if response.err != nil {
		t.Fatalf("run failed: %v", response.err)
	}
	if switchedTo == "" || switchedTo == startModel {
		t.Fatalf("SETUP INVALID: the escalation never switched providers (switched to %q), so nothing here exercises attribution", switchedTo)
	}
	if len(escalated.requests) == 0 {
		t.Fatal("SETUP INVALID: the escalated provider was never asked for a completion")
	}
	if len(response.usageEvents) != 2 {
		t.Fatalf("usage events = %d, want one before the switch and one after", len(response.usageEvents))
	}

	want := []string{startModel, switchedTo}
	if !reflect.DeepEqual(live, want) {
		t.Fatalf("live usage attribution = %v, want %v", live, want)
	}
	if !reflect.DeepEqual(response.usageModelIDs, want) {
		t.Fatalf("per-event usage attribution on the response = %v, want %v", response.usageModelIDs, want)
	}
	for index := range response.usageEvents {
		if got := response.usageModelIDAt(index); got != want[index] {
			t.Fatalf("usageModelIDAt(%d) = %q, want %q", index, got, want[index])
		}
	}
}

// A response built without the per-event record, which is every constructor
// that predates escalation, still attributes through the run-level model.
func TestUsageModelIDAtFallsBackToTheRunModel(t *testing.T) {
	msg := agentResponseMsg{usageModelID: "gpt-4.1", usageEvents: make([]zeroruntime.Usage, 2)}
	for index := range msg.usageEvents {
		if got := msg.usageModelIDAt(index); got != "gpt-4.1" {
			t.Fatalf("usageModelIDAt(%d) = %q, want the run-level model", index, got)
		}
	}
	msg.usageModelIDs = []string{"gpt-4.1-mini"}
	if got := msg.usageModelIDAt(0); got != "gpt-4.1-mini" {
		t.Fatalf("usageModelIDAt(0) = %q, want the per-event model", got)
	}
	if got := msg.usageModelIDAt(1); got != "gpt-4.1" {
		t.Fatalf("usageModelIDAt(1) = %q, want the run-level fallback past the record", got)
	}
}
