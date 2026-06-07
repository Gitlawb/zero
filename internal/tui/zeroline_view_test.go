package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/tools"
)

func newZerolineModel() model {
	m := newModel(context.Background(), Options{
		Skin:         "zeroline",
		ThemeDark:    true,
		Cwd:          "/home/you/src/zero",
		ProviderName: "anthropic",
		ModelName:    "claude-sonnet-4.5",
	})
	m.width, m.height = 100, 30
	m.booted = true // skip the boot splash animation; these tests cover home/chat
	return m
}

func TestZerolineHomeThenChat(t *testing.T) {
	m := newZerolineModel()

	// Home is shown until the first turn (showSplash true).
	if !strings.Contains(m.View(), "Own your agent") {
		t.Fatal("expected Zen home before first turn")
	}

	// Simulate an in-progress run with a live transcript.
	m.showSplash = false
	m.pending = true
	m.transcript = []transcriptRow{
		{kind: rowUser, text: "refactor internal/agent/loop.go"},
		{kind: rowToolCall, id: "t1", tool: "grep", text: "tool call: grep", detail: "pattern: case"},
		{kind: rowToolResult, id: "t1", tool: "grep", status: tools.StatusOK, detail: "3 matches"},
	}
	chat := m.View()
	for _, want := range []string{"WORKING", "you", "grep", "claude-sonnet-4.5", "thinking"} {
		if !strings.Contains(chat, want) {
			t.Errorf("chat view missing %q", want)
		}
	}

	// once tokens stream, the live text shows instead of the thinking line
	m.streamingText = "here is the streamed answer"
	if s := m.View(); !strings.Contains(s, "here is the streamed answer") {
		t.Error("streaming text not rendered in chat view")
	}
}

func TestZerolineAskUserRender(t *testing.T) {
	m := newZerolineModel()
	m.showSplash = false
	m.pending = true
	m.activeRunID = 7

	updated, _ := m.Update(askUserRequestMsg{
		runID:   7,
		request: testAskUserRequest(),
		answer:  func([]string) {},
	})
	next := updated.(model)
	if next.pendingAskUser == nil {
		t.Fatal("expected ask_user prompt to be pending")
	}

	out := next.View()
	for _, want := range []string{"Which framework?", "React", "Vue", "question 1 of 2"} {
		if !strings.Contains(out, want) {
			t.Errorf("zeroline ask_user view missing %q", want)
		}
	}
	// The misleading "working…"/"thinking" spinner must be suppressed while a
	// questionnaire is pending.
	if strings.Contains(out, "thinking") {
		t.Error("zeroline ask_user view should suppress the thinking spinner")
	}
}

func TestZerolinePermissionRender(t *testing.T) {
	m := newZerolineModel()
	m.showSplash = false
	m.pending = true
	m.pendingPermission = &pendingPermissionPrompt{
		request: agent.PermissionRequest{ToolName: "edit_file", SideEffect: "write"},
	}
	out := m.View()
	for _, want := range []string{"BLOCKED", "permission required", "edit_file", "allow", "deny"} {
		if !strings.Contains(out, want) {
			t.Errorf("permission view missing %q", want)
		}
	}
}

func TestZerolineThemeKeys(t *testing.T) {
	m := newZerolineModel()
	// digit selects theme when input empty
	nm, handled := m.handleZerolineKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("3")})
	if !handled || nm.themeVariant != 2 {
		t.Fatalf("theme select failed handled=%v variant=%d", handled, nm.themeVariant)
	}
	// ctrl+l toggles light/dark
	nm2, handled2 := nm.handleZerolineKeys(tea.KeyMsg{Type: tea.KeyCtrlL})
	if !handled2 || nm2.themeDark == nm.themeDark {
		t.Fatal("ctrl+l did not toggle dark")
	}
}
