package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

type fakeProvider struct {
	events []zeroruntime.StreamEvent
}

func (provider *fakeProvider) StreamCompletion(
	ctx context.Context,
	request zeroruntime.CompletionRequest,
) (<-chan zeroruntime.StreamEvent, error) {
	ch := make(chan zeroruntime.StreamEvent, len(provider.events))
	for _, event := range provider.events {
		ch <- event
	}
	close(ch)
	return ch, nil
}

func TestParseCommand(t *testing.T) {
	cases := []struct {
		input string
		kind  commandKind
		text  string
	}{
		{input: "", kind: commandEmpty},
		{input: "   ", kind: commandEmpty},
		{input: "/help", kind: commandHelp},
		{input: "/clear", kind: commandClear},
		{input: "/exit", kind: commandExit},
		{input: "/quit", kind: commandExit},
		{input: "/tools", kind: commandTools},
		{input: "/permissions", kind: commandPermissions},
		{input: "/context", kind: commandContext},
		{input: "/model", kind: commandModel},
		{input: "/model list", kind: commandModel, text: "list"},
		{input: "/debug-mode", kind: commandDebug},
		{input: "hello zero", kind: commandPrompt, text: "hello zero"},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			command := parseCommand(tc.input)
			if command.kind != tc.kind || command.text != tc.text {
				t.Fatalf("expected kind=%v text=%q, got kind=%v text=%q", tc.kind, tc.text, command.kind, command.text)
			}
		})
	}
}

func TestCommandRegistryResolvesAliasesAndFormatsHelp(t *testing.T) {
	names := listCommandNames()
	for _, name := range []string{"/help", "/model", "/provider", "/context", "/debug-mode", "/quit"} {
		if !stringSliceContains(names, name) {
			t.Fatalf("expected command names to contain %s, got %#v", name, names)
		}
	}

	resolved, ok := resolveCommand("/quit")
	if !ok || resolved.kind != commandExit {
		t.Fatalf("expected /quit to resolve to exit, got ok=%v command=%#v", ok, resolved)
	}

	help := strings.Join(formatCommandHelpLines(), "\n")
	for _, want := range []string{"/model", "/context", "/debug", "/permissions", "model"} {
		assertContains(t, help, want)
	}
}

func TestTranscriptReducer(t *testing.T) {
	transcript := initialTranscript()
	transcript = reduceTranscript(transcript, transcriptAction{kind: actionAppendUser, text: "hello"})
	transcript = reduceTranscript(transcript, transcriptAction{kind: actionAppendAssistant, text: "hi"})
	transcript = reduceTranscript(transcript, transcriptAction{kind: actionAppendToolCall, name: "read_file"})
	transcript = reduceTranscript(transcript, transcriptAction{kind: actionAppendToolResult, name: "read_file", text: "ok"})

	if len(transcript) != 5 {
		t.Fatalf("expected welcome plus four rows, got %#v", transcript)
	}
	if transcript[1].kind != rowUser || transcript[1].text != "hello" {
		t.Fatalf("expected user row, got %#v", transcript[1])
	}
	if transcript[3].kind != rowToolCall || !strings.Contains(transcript[3].text, "read_file") {
		t.Fatalf("expected tool-call placeholder, got %#v", transcript[3])
	}

	cleared := reduceTranscript(transcript, transcriptAction{kind: actionClear})
	if len(cleared) != 1 || cleared[0].kind != rowWelcome {
		t.Fatalf("expected clear to reset to welcome row, got %#v", cleared)
	}
}

func TestInitialRenderContainsHeaderInputAndFooter(t *testing.T) {
	model := newModel(context.Background(), Options{
		Cwd:          `D:\codings\Opensource\Zero`,
		ProviderName: "fake",
		ModelName:    "m-test",
	})

	view := model.View()
	assertContains(t, view, "ZERO")
	assertContains(t, view, `D:\codings\Opensource\Zero`)
	assertContains(t, view, "fake/m-test")
	assertContains(t, view, "zero >")
	assertContains(t, view, "/help")
	assertContains(t, view, "/clear")
	assertContains(t, view, "/exit")
	assertContains(t, view, "Ctrl+C")
}

func TestCommandFooterTextUsesRegistryEntries(t *testing.T) {
	footer := commandFooterText()

	for _, command := range []string{"/help", "/model", "/provider", "/context", "/tools", "/permissions", "/clear", "/exit"} {
		assertContains(t, footer, command)
	}
	assertContains(t, footer, "Esc clear")
	assertContains(t, footer, "Ctrl+C quit")
}

func TestCommandFooterTextFallsBackWhenRegistryIsEmpty(t *testing.T) {
	footer := formatCommandFooterText(nil)

	for _, command := range []string{"/help", "/model", "/provider", "/context", "/tools", "/permissions", "/clear", "/exit"} {
		assertContains(t, footer, command)
	}
	assertContains(t, footer, "Esc clear")
	assertContains(t, footer, "Ctrl+C quit")
}

func TestHelpCommandAppendsHelpRow(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.input.SetValue("/help")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)

	if !transcriptContains(next.transcript, "/tools") {
		t.Fatalf("expected help transcript to mention /tools, got %#v", next.transcript)
	}
	if !transcriptContains(next.transcript, "/model") || !transcriptContains(next.transcript, "/context") {
		t.Fatalf("expected help transcript to mention model and context commands, got %#v", next.transcript)
	}
}

func TestClearCommandResetsTranscript(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendUser, text: "hello"})
	m.input.SetValue("/clear")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)

	if len(next.transcript) != 1 || next.transcript[0].kind != rowWelcome {
		t.Fatalf("expected clear to reset transcript, got %#v", next.transcript)
	}
}

func TestToolsCommandListsRegisteredTools(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool("."))
	m := newModel(context.Background(), Options{Registry: registry})
	m.input.SetValue("/tools")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)

	if !transcriptContains(next.transcript, "read_file") {
		t.Fatalf("expected tools transcript to list read_file, got %#v", next.transcript)
	}
}

func TestPlanCommandShowsCurrentPlan(t *testing.T) {
	registry := tools.NewRegistry()
	planTool := tools.NewUpdatePlanTool()
	result := planTool.Run(context.Background(), map[string]any{
		"plan": []any{
			map[string]any{
				"id":      "one",
				"content": "Wire model catalog",
				"status":  "completed",
			},
			map[string]any{
				"id":      "two",
				"content": "Add max turns",
				"status":  "in_progress",
				"notes":   "Go exec parity",
			},
		},
	})
	if result.Status != tools.StatusOK {
		t.Fatalf("update_plan setup failed: %#v", result)
	}
	registry.Register(planTool)
	m := newModel(context.Background(), Options{Registry: registry})
	m.input.SetValue("/plan")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /plan to be handled without starting an agent run")
	}
	for _, want := range []string{"Current Plan", "Wire model catalog", "Add max turns", "in_progress", "Go exec parity"} {
		if !transcriptContains(next.transcript, want) {
			t.Fatalf("expected plan transcript to contain %q, got %#v", want, next.transcript)
		}
	}
}

func TestPlanCommandHandlesMissingPlanTool(t *testing.T) {
	m := newModel(context.Background(), Options{Registry: tools.NewRegistry()})
	m.input.SetValue("/plan")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)

	if !transcriptContains(next.transcript, "No plan is active") {
		t.Fatalf("expected missing plan message, got %#v", next.transcript)
	}
}

func TestContextCommandShowsSessionState(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool("."))
	m := newModel(context.Background(), Options{
		Cwd:            `D:\codings\Opensource\Zero`,
		ProviderName:   "openai",
		ModelName:      "gpt-4.1",
		Registry:       registry,
		PermissionMode: agent.PermissionModeAsk,
	})
	m.input.SetValue("/context")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /context to be handled without starting an agent run")
	}
	for _, want := range []string{
		`D:\codings\Opensource\Zero`,
		"provider: openai",
		"model: gpt-4.1",
		"permission mode: ask",
		"tools: 1",
	} {
		if !transcriptContains(next.transcript, want) {
			t.Fatalf("expected context transcript to contain %q, got %#v", want, next.transcript)
		}
	}
}

func TestModelCommandShowsActiveModelWithoutRunningAgent(t *testing.T) {
	m := newModel(context.Background(), Options{
		ProviderName: "openai",
		ModelName:    "gpt-4.1",
		Provider:     &fakeProvider{},
	})
	m.input.SetValue("/model list")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /model to be handled without starting an agent run")
	}
	for _, want := range []string{"Active model: gpt-4.1", "provider: openai", "Available models", "* gpt-4.1"} {
		if !transcriptContains(next.transcript, want) {
			t.Fatalf("expected model transcript to contain %q, got %#v", want, next.transcript)
		}
	}
	if !transcriptHasMarkedModelEntry(next.transcript) {
		t.Fatalf("expected model transcript to contain a marked model entry, got %#v", next.transcript)
	}
	if transcriptContains(next.transcript, "Model switching") {
		t.Fatalf("expected /model list to show catalog, got switching placeholder: %#v", next.transcript)
	}
}

func TestModelCommandKeepsSwitchingStatusExplicit(t *testing.T) {
	m := newModel(context.Background(), Options{
		ProviderName: "openai",
		ModelName:    "gpt-4.1",
		Provider:     &fakeProvider{},
	})
	m.input.SetValue("/model gpt-4.1-mini")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected /model to be handled without starting an agent run")
	}
	for _, want := range []string{"Active model: gpt-4.1", "provider: openai", "Model switching is not wired"} {
		if !transcriptContains(next.transcript, want) {
			t.Fatalf("expected model transcript to contain %q, got %#v", want, next.transcript)
		}
	}
}

func TestPromptSubmitAppendsUserAndAssistantRows(t *testing.T) {
	provider := &fakeProvider{events: []zeroruntime.StreamEvent{
		{Type: zeroruntime.StreamEventText, Content: "hello"},
		{Type: zeroruntime.StreamEventText, Content: " back"},
		{Type: zeroruntime.StreamEventDone},
	}}
	m := newModel(context.Background(), Options{
		Provider: provider,
		Registry: tools.NewRegistry(),
	})
	m.input.SetValue("say hi")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)
	if !transcriptContains(next.transcript, "say hi") {
		t.Fatalf("expected user row after submit, got %#v", next.transcript)
	}
	if cmd == nil {
		t.Fatal("expected submit to return agent command")
	}

	msg := cmd()
	updated, _ = next.Update(msg)
	next = updated.(model)
	if !transcriptContains(next.transcript, "hello back") {
		t.Fatalf("expected assistant row after agent response, got %#v", next.transcript)
	}
}

func TestPromptSubmitDoesNotStartAnotherRunWhilePending(t *testing.T) {
	m := newModel(context.Background(), Options{
		Provider: &fakeProvider{},
		Registry: tools.NewRegistry(),
	})
	m.pending = true
	m.input.SetValue("second prompt")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)

	if cmd != nil {
		t.Fatal("expected no command while another run is pending")
	}
	if transcriptContains(next.transcript, "second prompt") {
		t.Fatalf("pending prompt should not be appended, got %#v", next.transcript)
	}
	if !next.pending {
		t.Fatal("expected existing pending run to remain pending")
	}
}

func TestEscCancelsPendingRun(t *testing.T) {
	m := newModel(context.Background(), Options{})
	cancelled := false
	m.pending = true
	m.activeRunID = 1
	m.runCancel = func() { cancelled = true }

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	next := updated.(model)

	if !cancelled {
		t.Fatal("expected Esc to cancel pending run")
	}
	if next.pending {
		t.Fatal("expected Esc to clear pending state")
	}
	if next.activeRunID != 0 || next.runCancel != nil {
		t.Fatalf("expected active run state to clear, got id=%d cancel=%v", next.activeRunID, next.runCancel)
	}
}

func TestStaleAgentResponseAfterCancelIsIgnored(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.pending = false
	m.activeRunID = 0
	m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendUser, text: "new prompt"})

	updated, _ := m.Update(agentResponseMsg{
		runID: 1,
		rows:  []transcriptRow{{kind: rowAssistant, text: "stale response"}},
	})
	next := updated.(model)

	if transcriptContains(next.transcript, "stale response") {
		t.Fatalf("stale response should be ignored, got %#v", next.transcript)
	}
}

func TestToolResultRowDefaultsEmptyStatusToOK(t *testing.T) {
	text := toolResultRowText(agent.ToolResult{Name: "read_file", Output: "done"})

	if !strings.Contains(text, "read_file ok done") {
		t.Fatalf("expected empty status to render as ok, got %q", text)
	}
}

func TestCtrlCExits(t *testing.T) {
	m := newModel(context.Background(), Options{})

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	next := updated.(model)

	if !next.exiting {
		t.Fatal("expected Ctrl+C to mark model exiting")
	}
	if cmd == nil {
		t.Fatal("expected Ctrl+C to return quit command")
	}
}

func assertContains(t *testing.T, text string, want string) {
	t.Helper()

	if !strings.Contains(text, want) {
		t.Fatalf("expected %q to contain %q", text, want)
	}
}

func transcriptContains(rows []transcriptRow, want string) bool {
	for _, row := range rows {
		if strings.Contains(row.text, want) {
			return true
		}
	}
	return false
}

func transcriptHasMarkedModelEntry(rows []transcriptRow) bool {
	for _, row := range rows {
		for _, line := range strings.Split(row.text, "\n") {
			if strings.HasPrefix(line, "* ") && strings.Contains(line, " (") {
				return true
			}
		}
	}
	return false
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
