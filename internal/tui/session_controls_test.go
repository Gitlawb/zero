package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/modelregistry"
	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

func TestEffortCommandListsAndSetsSupportedEffort(t *testing.T) {
	m := newModel(context.Background(), Options{ModelName: "claude-sonnet-4.5"})
	m.input.SetValue("/effort list")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /effort list to be handled without starting an agent run")
	}
	for _, want := range []string{"Effort", "active effort: auto", "available: low, medium, high"} {
		if !transcriptContains(next.transcript, want) {
			t.Fatalf("expected effort transcript to contain %q, got %#v", want, next.transcript)
		}
	}

	next.input.SetValue("/effort high")
	updated, cmd = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(model)

	if cmd != nil {
		t.Fatal("expected /effort high to be handled without starting an agent run")
	}
	if next.reasoningEffort != modelregistry.ReasoningEffortHigh {
		t.Fatalf("expected effort high, got %q", next.reasoningEffort)
	}
	if !transcriptContains(next.transcript, "active effort: high") {
		t.Fatalf("expected effort switch transcript, got %#v", next.transcript)
	}
}

func TestEffortCommandRejectsUnsupportedActiveModel(t *testing.T) {
	m := newModel(context.Background(), Options{ModelName: "gpt-4.1"})
	m.input.SetValue("/effort high")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)

	if next.reasoningEffort != "" {
		t.Fatalf("expected effort to remain auto, got %q", next.reasoningEffort)
	}
	if !transcriptContains(next.transcript, "does not expose reasoning effort controls") {
		t.Fatalf("expected unsupported model message, got %#v", next.transcript)
	}
}

func TestStyleCommandListsAndSetsSessionPreference(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.input.SetValue("/style")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /style to be handled without starting an agent run")
	}
	if !transcriptContains(next.transcript, "active style: balanced") || !transcriptContains(next.transcript, "concise") {
		t.Fatalf("expected style list transcript, got %#v", next.transcript)
	}

	next.input.SetValue("/style concise")
	updated, cmd = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(model)

	if cmd != nil {
		t.Fatal("expected /style concise to be handled without starting an agent run")
	}
	if next.responseStyle != "concise" {
		t.Fatalf("expected concise style, got %q", next.responseStyle)
	}
	if !transcriptContains(next.transcript, "active style: concise") {
		t.Fatalf("expected style switch transcript, got %#v", next.transcript)
	}
}

func TestCompactCommandRecordsShellRequest(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.input.SetValue("/compact")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /compact to be handled without starting an agent run")
	}
	if next.compactRequests != 1 {
		t.Fatalf("expected one compact request, got %d", next.compactRequests)
	}
	for _, want := range []string{"Compact", "requested, not yet compacted", "future compaction backend"} {
		if !transcriptContains(next.transcript, want) {
			t.Fatalf("expected compact transcript to contain %q, got %#v", want, next.transcript)
		}
	}

	next.input.SetValue("/compact status")
	updated, cmd = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(model)

	if cmd != nil {
		t.Fatal("expected /compact status to be handled without starting an agent run")
	}
	if next.compactRequests != 1 {
		t.Fatalf("status should not add compact requests, got %d", next.compactRequests)
	}
}

func TestUsageEventsUpdateFooterAndContext(t *testing.T) {
	provider := &fakeProvider{events: []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventText, Content: "done"},
		{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 100, CachedInputTokens: 25, OutputTokens: 20}},
		{Type: zeroruntime.StreamEventDone},
	}}
	m := newModel(context.Background(), Options{
		ModelName: "gpt-4.1",
		Provider:  provider,
		Registry:  tools.NewRegistry(),
	})
	m.input.SetValue("track usage")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)
	if cmd == nil {
		t.Fatal("expected prompt to start agent run")
	}
	updated, _ = next.Update(cmd())
	next = updated.(model)

	footer := next.footerText()
	for _, want := range []string{"ready", "gpt-4.1", "120 tokens", "$"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("expected footer to contain %q, got %q", want, footer)
		}
	}

	next.input.SetValue("/context")
	updated, cmd = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(model)

	if cmd != nil {
		t.Fatal("expected /context to be handled without starting an agent run")
	}
	for _, want := range []string{"usage: 1 request, 120 tokens", "style: balanced", "effort: auto", "compaction: not compacted"} {
		if !transcriptContains(next.transcript, want) {
			t.Fatalf("expected context transcript to contain %q, got %#v", want, next.transcript)
		}
	}
}

func TestUsageEventsForwardExistingAgentCallback(t *testing.T) {
	provider := &fakeProvider{events: []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 10, OutputTokens: 5}},
		{Type: zeroruntime.StreamEventText, Content: "done"},
		{Type: zeroruntime.StreamEventDone},
	}}
	seen := []zeroruntime.Usage{}
	m := newModel(context.Background(), Options{
		ModelName: "gpt-4.1",
		Provider:  provider,
		Registry:  tools.NewRegistry(),
		AgentOptions: agent.Options{
			OnUsage: func(event agent.Usage) {
				seen = append(seen, event)
			},
		},
	})
	m.input.SetValue("track usage")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)
	if cmd == nil {
		t.Fatal("expected prompt to start agent run")
	}
	msg := cmd()
	if len(seen) != 1 || seen[0].TotalTokens() != 15 {
		t.Fatalf("expected original usage callback to receive event, got %#v", seen)
	}
	updated, _ = next.Update(msg)
	next = updated.(model)

	if !strings.Contains(next.footerText(), "15 tokens") {
		t.Fatalf("expected usage to still update footer, got %q", next.footerText())
	}
}

func TestUsageEventsForCustomModelUseTokenOnlyFallback(t *testing.T) {
	provider := &fakeProvider{events: []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventText, Content: "done"},
		{Type: zeroruntime.StreamEventUsage, Usage: zeroruntime.Usage{InputTokens: 100, OutputTokens: 20}},
		{Type: zeroruntime.StreamEventDone},
	}}
	m := newModel(context.Background(), Options{
		ModelName: "custom-coder",
		Provider:  provider,
		Registry:  tools.NewRegistry(),
	})
	m.input.SetValue("track usage")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)
	if cmd == nil {
		t.Fatal("expected prompt to start agent run")
	}
	updated, _ = next.Update(cmd())
	next = updated.(model)

	footer := next.footerText()
	for _, want := range []string{"custom-coder", "1 request, 120 tokens", "cost unavailable"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("expected footer to contain %q, got %q", want, footer)
		}
	}
	if transcriptContains(next.transcript, "usage:") {
		t.Fatalf("custom model usage should not append a transcript error, got %#v", next.transcript)
	}
}

func TestStaleAgentUsageResponseIsIgnored(t *testing.T) {
	m := newModel(context.Background(), Options{ModelName: "gpt-4.1"})

	updated, _ := m.Update(agentResponseMsg{
		runID:        42,
		usageModelID: "gpt-4.1",
		usageEvents:  []zeroruntime.Usage{{InputTokens: 100, OutputTokens: 20}},
	})
	next := updated.(model)

	if strings.Contains(next.footerText(), "120 tokens") {
		t.Fatalf("stale usage response should be ignored, got footer %q", next.footerText())
	}
}

func TestModelSwitchClearsUnsupportedEffortPreference(t *testing.T) {
	nextProvider := &fakeProvider{}
	m := newModel(context.Background(), Options{
		ProviderName:    "openai",
		ModelName:       "gpt-4.1-mini",
		ReasoningEffort: modelregistry.ReasoningEffortHigh,
		Provider:        &fakeProvider{},
		ProviderProfile: openAITestProfile("gpt-4.1-mini"),
		NewProvider: func(profile config.ProviderProfile) (zeroruntime.Provider, error) {
			if profile.Model != "gpt-4.1" {
				t.Fatalf("expected provider rebuild for gpt-4.1, got %#v", profile)
			}
			return nextProvider, nil
		},
	})
	m.input.SetValue("/model gpt-4.1")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /model to be handled without starting an agent run")
	}
	if next.reasoningEffort != "" {
		t.Fatalf("expected unsupported effort preference to reset, got %q", next.reasoningEffort)
	}
	if !transcriptContains(next.transcript, "unsupported preference reset") {
		t.Fatalf("expected model switch transcript to mention effort reset, got %#v", next.transcript)
	}
}

func openAITestProfile(modelID string) config.ProviderProfile {
	return config.ProviderProfile{
		Name:         "openai",
		ProviderKind: config.ProviderKindOpenAI,
		BaseURL:      config.OpenAIBaseURL,
		APIKey:       "sk-test",
		Model:        modelID,
	}
}
