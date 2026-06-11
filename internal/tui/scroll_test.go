package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestMouseWheelScrollsChatWithoutRecallingInputHistory(t *testing.T) {
	m := newModel(context.Background(), Options{AltScreen: true})
	m.width = 90
	m.height = 14
	m.mouseCapture = true
	m.inputHistory = []string{"old prompt"}
	m.historyIdx = len(m.inputHistory)
	for index := 0; index < 12; index++ {
		m.transcript = appendRow(m.transcript, rowAssistant, "message "+string(rune('A'+index)))
	}

	updated, cmd := m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp})
	m = updated.(model)
	if cmd != nil {
		t.Fatal("mouse wheel should not return a command")
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("mouse wheel should not recall input history, got %q", got)
	}
	if m.chatScrollOffset != chatWheelScrollLines {
		t.Fatalf("chatScrollOffset = %d, want %d", m.chatScrollOffset, chatWheelScrollLines)
	}
}

func TestAltScreenTranscriptScrollKeepsFooterFixed(t *testing.T) {
	m := newModel(context.Background(), Options{AltScreen: true, ProviderName: "openai", ModelName: "gpt-4.1"})
	m.width = 90
	m.height = 10
	for index := 0; index < 14; index++ {
		m.transcript = appendRow(m.transcript, rowAssistant, "message "+string(rune('A'+index)))
	}

	bottom := plainRender(t, m.View())
	if strings.Contains(bottom, "message A") {
		t.Fatalf("bottom view should start near recent history, got:\n%s", bottom)
	}
	if !strings.Contains(bottom, "describe a task for zero") || !strings.Contains(bottom, "openai") {
		t.Fatalf("bottom view should keep composer/status fixed, got:\n%s", bottom)
	}

	m = m.scrollChat(80)
	scrolled := plainRender(t, m.View())
	if !strings.Contains(scrolled, "message A") {
		t.Fatalf("scrolled view should reveal older history, got:\n%s", scrolled)
	}
	if !strings.Contains(scrolled, "describe a task for zero") || !strings.Contains(scrolled, "openai") {
		t.Fatalf("scrolled view should keep composer/status fixed, got:\n%s", scrolled)
	}
}

func TestAltScreenTranscriptClampsFooterToTerminalHeight(t *testing.T) {
	m := newModel(context.Background(), Options{AltScreen: true, ProviderName: "openai", ModelName: "gpt-4.1"})
	m.width = 80
	m.height = 3
	m.copyStatus = "Copied!"
	m.transcript = appendRow(m.transcript, rowAssistant, "hello")

	view := plainRender(t, m.View())
	if got := len(viewLines(view)); got > m.height {
		t.Fatalf("view rendered %d lines, want at most terminal height %d:\n%s", got, m.height, view)
	}
}

func TestEmptySubmitKeepsChatScrollOffset(t *testing.T) {
	m := newModel(context.Background(), Options{AltScreen: true})
	m.width = 90
	m.height = 14
	for index := 0; index < 12; index++ {
		m.transcript = appendRow(m.transcript, rowAssistant, "message "+string(rune('A'+index)))
	}

	// Scroll up, then press Enter on an empty composer: the no-op submit must not
	// yank the viewport back to the bottom.
	m.chatScrollOffset = 7
	m.input.SetValue("")
	updated, _ := m.handleSubmit()
	m = updated.(model)
	if m.chatScrollOffset != 7 {
		t.Fatalf("empty submit changed chatScrollOffset to %d, want it left at 7", m.chatScrollOffset)
	}

	// A real submission (here a slash command) still snaps back to the bottom.
	m.chatScrollOffset = 7
	m.input.SetValue("/help")
	updated, _ = m.handleSubmit()
	m = updated.(model)
	if m.chatScrollOffset != 0 {
		t.Fatalf("real submit chatScrollOffset = %d, want 0", m.chatScrollOffset)
	}
}

func TestPageKeysScrollAltScreenTranscript(t *testing.T) {
	m := newModel(context.Background(), Options{AltScreen: true})
	m.width = 90
	m.height = 20

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = updated.(model)
	if m.chatScrollOffset != m.chatPageScrollLines() {
		t.Fatalf("page up offset = %d, want %d", m.chatScrollOffset, m.chatPageScrollLines())
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = updated.(model)
	if m.chatScrollOffset != 0 {
		t.Fatalf("page down should return to bottom, got offset %d", m.chatScrollOffset)
	}
}
