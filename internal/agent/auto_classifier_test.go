package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/zeroruntime"
)

type autoClassifierRecordingProvider struct {
	request zeroruntime.CompletionRequest
	text    string
}

func (p *autoClassifierRecordingProvider) StreamCompletion(_ context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	p.request = request
	ch := make(chan zeroruntime.StreamEvent, 2)
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventText, Content: p.text}
	ch <- zeroruntime.StreamEvent{Type: zeroruntime.StreamEventDone}
	close(ch)
	return ch, nil
}

func TestAutoPermissionClassifierUsesEmbeddedPrompt(t *testing.T) {
	provider := &autoClassifierRecordingProvider{text: `{"action":"allow","reason":"low-risk workspace read"}`}

	decision, err := classifyAutoPermissionWithProvider(context.Background(), provider, AutoPermissionClassifierRequest{
		ToolName:       "read_file",
		PermissionMode: PermissionModeAutoClassifier,
	})
	if err != nil {
		t.Fatalf("classifyAutoPermissionWithProvider returned error: %v", err)
	}
	if decision.Action != AutoPermissionClassifierAllow {
		t.Fatalf("decision action = %q, want %q", decision.Action, AutoPermissionClassifierAllow)
	}
	if len(provider.request.Messages) != 2 {
		t.Fatalf("provider got %d messages, want 2", len(provider.request.Messages))
	}
	system := provider.request.Messages[0]
	if system.Role != zeroruntime.MessageRoleSystem {
		t.Fatalf("first message role = %q, want system", system.Role)
	}
	if !strings.Contains(system.Content, "Zero's auto-classifier permission reviewer") {
		t.Fatalf("system prompt missing classifier identity: %q", system.Content)
	}
	if !strings.Contains(system.Content, "strict JSON only") {
		t.Fatalf("system prompt missing response contract: %q", system.Content)
	}
	if strings.TrimSpace(autoPermissionClassifierSystemPrompt) == "" {
		t.Fatal("embedded auto-classifier prompt is empty")
	}
	if len(provider.request.Tools) != 0 {
		t.Fatalf("classifier request exposed %d tools, want none", len(provider.request.Tools))
	}
}

func TestParseAutoPermissionClassifierDecision(t *testing.T) {
	t.Run("valid allow", func(t *testing.T) {
		decision, ok := parseAutoPermissionClassifierDecision(`{"action":"allow","reason":"safe"}`)
		if !ok || decision.Action != AutoPermissionClassifierAllow || decision.Reason != "safe" {
			t.Fatalf("parse = (%#v,%t), want allow/safe", decision, ok)
		}
	})
	t.Run("valid prompt", func(t *testing.T) {
		decision, ok := parseAutoPermissionClassifierDecision(`{"action":"prompt","reason":"unsure"}`)
		if !ok || decision.Action != AutoPermissionClassifierPrompt || decision.Reason != "unsure" {
			t.Fatalf("parse = (%#v,%t), want prompt/unsure", decision, ok)
		}
	})
	t.Run("fenced json", func(t *testing.T) {
		decision, ok := parseAutoPermissionClassifierDecision("```json\n{\"action\":\"allow\",\"reason\":\"safe\"}\n```")
		if !ok || decision.Action != AutoPermissionClassifierAllow || decision.Reason != "safe" {
			t.Fatalf("parse = (%#v,%t), want allow/safe", decision, ok)
		}
	})

	rejects := map[string]string{
		"empty":            ``,
		"unknown action":   `{"action":"maybe","reason":"x"}`,
		"empty reason":     `{"action":"allow","reason":""}`,
		"trailing content": `{"action":"allow","reason":"x"} trailing`,
		"unknown field":    `{"action":"allow","reason":"x","extra":true}`,
		"not an object":    `"allow"`,
	}
	for name, output := range rejects {
		t.Run(name, func(t *testing.T) {
			if _, ok := parseAutoPermissionClassifierDecision(output); ok {
				t.Fatalf("expected %q to be rejected", output)
			}
		})
	}
}
