package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestEmptyStateShowsBrandTaglineAndSuggestions(t *testing.T) {
	m := newModel(context.Background(), Options{ProviderName: "anthropic", ModelName: "claude-sonnet-4.5"})
	m.width, m.height = 100, 30

	view := plainRender(t, m.View())
	assertContains(t, view, "███████╗███████╗██████╗  ██████╗")
	assertContains(t, view, emptyStateTagline)
	assertContains(t, view, "running zero against ")
	assertContains(t, view, "anthropic/claude-sonnet-4.5")
	for _, suggestion := range emptyStateSuggestions {
		assertContains(t, view, suggestion)
	}
}

func TestEmptyStateDisappearsAfterFirstRow(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.width, m.height = 100, 30
	m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendUser, text: "hello"})

	view := m.View()
	if strings.Contains(view, emptyStateTagline) {
		t.Fatal("empty state must disappear once the transcript has content")
	}
	assertContains(t, view, "hello")
}

func TestSuggestionKeysInsertOnEmptySurface(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.width, m.height = 100, 30

	m = typeRunes(t, m, "2")
	if got := m.input.Value(); got != emptyStateSuggestions[1] {
		t.Fatalf("pressing 2 on the empty surface should insert suggestion 2, got %q", got)
	}

	// With text already in the composer the digit types normally.
	m.input.SetValue("count to ")
	m = typeRunes(t, m, "3")
	if got := m.input.Value(); got != "count to 3" {
		t.Fatalf("digit should append to a non-empty composer, got %q", got)
	}

	// Once the transcript has content, a bare digit types normally too.
	fresh := newModel(context.Background(), Options{})
	fresh.transcript = reduceTranscript(fresh.transcript, transcriptAction{kind: actionAppendUser, text: "hi"})
	fresh = typeRunes(t, fresh, "1")
	if got := fresh.input.Value(); got != "1" {
		t.Fatalf("digit should type normally after the first turn, got %q", got)
	}
}

func TestBorderedBlockFitsLongPlainLines(t *testing.T) {
	block := borderedBlock(24, []string{"this line should truncate inside the border"})

	assertContains(t, block, "\u2026")
	assertRenderedLineWidths(t, block, 24)
}

func TestBorderedBlockFitsLongStyledLines(t *testing.T) {
	block := borderedBlock(26, []string{
		zeroTheme.accent.Render("styled line should truncate inside the border"),
	})

	assertContains(t, block, "\u2026")
	assertRenderedLineWidths(t, block, 26)
}

func assertRenderedLineWidths(t *testing.T, block string, width int) {
	t.Helper()

	for _, line := range strings.Split(block, "\n") {
		if got := lipgloss.Width(line); got != width {
			t.Fatalf("expected line width %d, got %d for %q", width, got, line)
		}
	}
}
