package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Gitlawb/zero/internal/tools"
)

func TestTranscriptCommandTogglesDetailedView(t *testing.T) {
	m := transcriptViewTestModel()
	m.input.SetValue("/transcript")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)
	assertContains(t, plainRender(t, next.View()), "Transcript")

	next.input.SetValue("/transcript")
	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(model)
	assertNotContains(t, plainRender(t, next.View()), "Transcript")
}

func TestCtrlOTogglesDetailedTranscriptView(t *testing.T) {
	m := transcriptViewTestModel()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	next := updated.(model)
	assertContains(t, plainRender(t, next.View()), "Transcript")

	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	next = updated.(model)
	assertNotContains(t, plainRender(t, next.View()), "Transcript")
}

func TestEscExitsDetailedTranscriptView(t *testing.T) {
	m := transcriptViewTestModel()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	next := updated.(model)
	assertContains(t, plainRender(t, next.View()), "Transcript")

	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyEsc})
	next = updated.(model)
	assertNotContains(t, plainRender(t, next.View()), "Transcript")
}

func TestDetailedTranscriptIncludesToolOutputBeyondLiveCap(t *testing.T) {
	m := transcriptViewTestModel()
	output := numberedLines(flushCardBodyMaxLines + 4)
	row := transcriptRow{kind: rowToolResult, id: "tool-1", tool: "custom_tool", status: tools.StatusOK, detail: output}
	m.transcript = append(m.transcript, row)
	m.flushed = len(m.transcript)

	compact := plainRender(t, m.renderRow(row, m.width, buildRowContext(m.transcript)))
	assertNotContains(t, compact, "line-020")
	assertContains(t, compact, "more lines")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	next := updated.(model)
	view := plainRender(t, next.View())
	assertContains(t, view, "line-404")
	assertNotContains(t, view, "more lines")
}

func TestDetailedTranscriptViewNeverExceedsTerminalWidth(t *testing.T) {
	for _, width := range []int{24, 40, 58, 80, 120} {
		m := transcriptViewTestModel()
		m.width = width
		m.transcript = append(m.transcript,
			transcriptRow{kind: rowUser, text: "please inspect this very long request and show the transcript without overflowing"},
			transcriptRow{kind: rowToolResult, id: "wide", tool: "custom_tool", status: tools.StatusOK, detail: strings.Repeat("wide-output-", 30)},
			transcriptRow{kind: rowAssistant, text: "done with a final answer that also needs wrapping", final: true, turnTools: 1},
		)
		m.flushed = len(m.transcript)

		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
		next := updated.(model)
		view := next.View()
		assertContains(t, plainRender(t, view), "Transcript")
		for index, line := range strings.Split(view, "\n") {
			if got := lipgloss.Width(line); got > chatWidth(width) {
				t.Fatalf("width %d: detailed transcript line %d is %d cells wide: %q", width, index, got, line)
			}
		}
	}
}

func TestDetailedTranscriptSwallowsNormalChatSubmit(t *testing.T) {
	m := transcriptViewTestModel()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	next := updated.(model)
	next.input.SetValue("this should not launch")

	updated, cmd := next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(model)

	if cmd != nil {
		t.Fatal("detailed transcript should not return a run command for Enter")
	}
	if transcriptContains(next.transcript, "this should not launch") {
		t.Fatalf("detailed transcript should not submit composer text, got %#v", next.transcript)
	}
	assertContains(t, plainRender(t, next.View()), "Transcript")
}

func transcriptViewTestModel() model {
	m := newModel(context.Background(), Options{
		Cwd:          "/work/zero",
		ProviderName: "openai",
		ModelName:    "gpt-test",
	})
	m.width = 96
	m.height = 30
	m.headerPrinted = true
	return m
}

func numberedLines(count int) string {
	lines := make([]string, 0, count)
	for i := 1; i <= count; i++ {
		lines = append(lines, fmt.Sprintf("line-%03d", i))
	}
	return strings.Join(lines, "\n")
}
