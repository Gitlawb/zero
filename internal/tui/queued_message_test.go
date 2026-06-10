package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

func TestEnterWhilePendingQueuesPromptWithoutStartingRun(t *testing.T) {
	m := newQueuedMessageTestModel(t)
	m.pending = true
	m.activeRunID = 1
	m.runID = 1
	m.input.SetValue("second prompt")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected queued prompt not to start another run immediately")
	}
	if !next.pending {
		t.Fatal("expected existing run to remain pending")
	}
	if got := next.input.Value(); got != "" {
		t.Fatalf("expected queued prompt to clear composer, got %q", got)
	}
	if transcriptContains(next.transcript, "second prompt") {
		t.Fatalf("queued prompt should not append to transcript before it runs, got %#v", next.transcript)
	}
}

func TestQueuedPromptPreviewAppearsInView(t *testing.T) {
	m := newQueuedMessageTestModel(t)
	m.pending = true
	m.activeRunID = 1
	m.runID = 1
	m.width = 96
	m.input.SetValue("summarize the failing test output")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)

	view := next.View()
	for _, want := range []string{"queued", "summarize the failing test output"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected queued prompt preview to contain %q, got:\n%s", want, view)
		}
	}
}

func TestAgentResponseLaunchesQueuedPrompt(t *testing.T) {
	provider := &scriptedProvider{scripts: [][]zeroruntime.StreamEvent{{
		{Type: zeroruntime.StreamEventText, Content: "queued answer"},
		{Type: zeroruntime.StreamEventDone},
	}}}
	m := newQueuedMessageTestModel(t)
	m.provider = provider
	m.pending = true
	m.activeRunID = 7
	m.runID = 7
	m.input.SetValue("run queued followup")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)
	if cmd != nil {
		t.Fatal("expected queued prompt not to launch until active run completes")
	}

	updated, cmd = next.Update(agentResponseMsg{runID: 7})
	next = updated.(model)

	if cmd == nil {
		t.Fatal("expected queued prompt to launch after active run completes")
	}
	if !next.pending {
		t.Fatal("expected queued prompt launch to mark model pending")
	}
	_ = execCmd(cmd)
	if len(provider.requests) != 1 {
		t.Fatalf("expected queued prompt to make one provider request, got %d", len(provider.requests))
	}
	messages := provider.requests[0].Messages
	if len(messages) == 0 || !strings.Contains(messages[len(messages)-1].Content, "run queued followup") {
		t.Fatalf("expected provider request to contain queued prompt, got %#v", messages)
	}
}

func TestEscClearsQueuedPromptBeforeCancelingRun(t *testing.T) {
	m := newQueuedMessageTestModel(t)
	cancelled := false
	m.pending = true
	m.activeRunID = 1
	m.runID = 1
	m.runCancel = func() { cancelled = true }
	m.width = 96
	m.input.SetValue("queued followup")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)

	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyEsc})
	next = updated.(model)

	if cancelled {
		t.Fatal("expected Esc to clear queued prompt before canceling the active run")
	}
	if !next.pending {
		t.Fatal("expected active run to remain pending after clearing queued prompt")
	}
	if strings.Contains(next.View(), "queued followup") {
		t.Fatalf("expected queued prompt preview to clear, got:\n%s", next.View())
	}
	if transcriptContains(next.transcript, "Run cancelled.") {
		t.Fatalf("expected Esc not to cancel run while clearing queued prompt, got %#v", next.transcript)
	}
}

func TestEnterWhileExitingDoesNotQueuePrompt(t *testing.T) {
	m := newQueuedMessageTestModel(t)
	m.pending = true
	m.exiting = true
	m.activeRunID = 1
	m.runID = 1
	m.input.SetValue("do not run during shutdown")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected submit during deferred exit to stay idle")
	}
	if next.hasQueuedMessage() {
		t.Fatalf("expected no queued prompt while exiting, got %q", next.queuedMessage)
	}
	if got := next.input.Value(); got != "do not run during shutdown" {
		t.Fatalf("expected composer to remain untouched, got %q", got)
	}
}

func TestAgentResponseLaunchesQueuedPromptAfterError(t *testing.T) {
	provider := &scriptedProvider{scripts: [][]zeroruntime.StreamEvent{{
		{Type: zeroruntime.StreamEventText, Content: "queued answer"},
		{Type: zeroruntime.StreamEventDone},
	}}}
	m := newQueuedMessageTestModel(t)
	m.provider = provider
	m.pending = true
	m.activeRunID = 9
	m.runID = 9
	m.input.SetValue("retry with more detail")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)

	updated, cmd := next.Update(agentResponseMsg{runID: 9, err: errors.New("first run failed")})

	if cmd == nil {
		t.Fatal("expected queued prompt to launch after active run errors")
	}
	next = updated.(model)
	if !next.pending {
		t.Fatal("expected queued prompt launch after error to mark model pending")
	}
}

func newQueuedMessageTestModel(t *testing.T) model {
	t.Helper()

	return newModel(context.Background(), Options{
		ProviderName: "openai",
		ModelName:    "gpt-4.1",
		Provider: &fakeProvider{events: []zeroruntime.StreamEvent{
			{Type: zeroruntime.StreamEventDone},
		}},
		Registry:     tools.NewRegistry(),
		SessionStore: testSessionStore(t),
	})
}
