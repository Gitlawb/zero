package tui

import (
	"context"
	"strings"
	"testing"

	glamourstyles "github.com/charmbracelet/glamour/styles"
	xansi "github.com/charmbracelet/x/ansi"
)

const glamourSampleMarkdown = "Here's the plan.\n\n" +
	"### Plan\n" +
	"- Parse **v2** tokens, fall back to `v1`\n" +
	"- Keep the `401` message specific\n\n" +
	"```go\n" +
	"func Authenticate(token string) error {\n" +
	"    return nil\n" +
	"}\n" +
	"```\n"

// TestAssistantGlamourLinesRenderMarkdown verifies the glamour path produces
// aligned display/plain lines, ANSI-free plain text, and preserves the markdown's
// content (heading, list, inline code, fenced code) across the blocks.
func TestAssistantGlamourLinesRenderMarkdown(t *testing.T) {
	clearGlamourCaches()
	display, plain := assistantGlamourLines(glamourSampleMarkdown, 80)

	if len(display) != len(plain) {
		t.Fatalf("display/plain length mismatch: %d vs %d", len(display), len(plain))
	}
	if len(plain) < 5 {
		t.Fatalf("expected multi-block render, got %d lines: %#v", len(plain), plain)
	}
	for i, p := range plain {
		if stripped := xansi.Strip(p); stripped != p {
			t.Errorf("plain line %d still carries ANSI: %q", i, p)
		}
	}
	joined := strings.Join(plain, "\n")
	for _, want := range []string{"Plan", "Parse", "v2", "v1", "401", "Authenticate", "token"} {
		if !strings.Contains(joined, want) {
			t.Errorf("rendered markdown missing %q\n---\n%s", want, joined)
		}
	}
	// No rendered line may exceed the wrap measure (left-aligned, width-correct).
	measure := assistantMeasure(80)
	for i, line := range display {
		if w := xansi.StringWidth(line); w > measure {
			t.Errorf("display line %d width %d > measure %d: %q", i, w, measure, line)
		}
	}
}

// TestAssistantGlamourLinesCacheAndClear checks memoization returns identical
// slices and that a theme swap (clearGlamourCaches) forces a fresh render.
func TestAssistantGlamourLinesCacheAndClear(t *testing.T) {
	clearGlamourCaches()
	d1, p1 := assistantGlamourLines(glamourSampleMarkdown, 72)
	d2, p2 := assistantGlamourLines(glamourSampleMarkdown, 72)
	if &d1[0] != &d2[0] || &p1[0] != &p2[0] {
		t.Fatal("expected cached slices to be reused on the second call")
	}
	clearGlamourCaches()
	d3, _ := assistantGlamourLines(glamourSampleMarkdown, 72)
	if len(d3) != len(d1) {
		t.Fatalf("post-clear render length changed: %d vs %d", len(d3), len(d1))
	}
}

// TestGlamourStyleConfigUsesPalette asserts the glamour StyleConfig is recolored
// from the active palette (lime accents, white strong, flush-left document) rather
// than the stock dark theme — no hex literals, all from zeroPalette.
func TestGlamourStyleConfigUsesPalette(t *testing.T) {
	s := glamourStyleConfig(glamourstyles.DarkStyleConfig, darkPalette)
	if s.Heading.Color == nil || *s.Heading.Color != darkPalette.accent {
		t.Errorf("heading color = %v, want accent %s", s.Heading.Color, darkPalette.accent)
	}
	if s.Strong.Color == nil || *s.Strong.Color != darkPalette.white {
		t.Errorf("strong color = %v, want white %s", s.Strong.Color, darkPalette.white)
	}
	if s.Item.Color == nil || *s.Item.Color != darkPalette.accent {
		t.Errorf("list bullet color = %v, want accent %s", s.Item.Color, darkPalette.accent)
	}
	if s.Document.Margin == nil || *s.Document.Margin != 0 {
		t.Errorf("document margin = %v, want flush-left 0", s.Document.Margin)
	}
	// The shared stock base must remain unmutated (we only replace pointers).
	if glamourstyles.DarkStyleConfig.Document.Margin != nil && *glamourstyles.DarkStyleConfig.Document.Margin == 0 {
		t.Error("glamourStyleConfig mutated the shared DarkStyleConfig base")
	}
}

// TestSelectableAssistantGlamourRowAligned verifies the final-row selection path:
// selectable metadata is derived from the same glamour pass (so display line N maps
// to plain selectable N), bodyY is sequential, copy text is ANSI-free, and output
// is stable across renders (cache-backed) when selection is inactive.
func TestSelectableAssistantGlamourRowAligned(t *testing.T) {
	clearGlamourCaches()
	m := newModel(context.Background(), Options{})
	row := transcriptRow{kind: rowAssistant, text: glamourSampleMarkdown, final: true, turnTools: 1}

	first, selectable := m.renderSelectableAssistantRow(0, row, 80, 5)
	if len(selectable) == 0 {
		t.Fatal("expected selectable lines for a final assistant row")
	}
	for i, line := range selectable {
		if line.bodyY != 5+i {
			t.Errorf("selectable[%d].bodyY = %d, want %d", i, line.bodyY, 5+i)
		}
		if stripped := xansi.Strip(line.text); stripped != line.text {
			t.Errorf("selectable[%d].text carries ANSI: %q", i, line.text)
		}
	}
	// Display includes the rendered lines plus the trailing completion line, so it
	// is at least one taller than the selectable (content) lines.
	displayLines := strings.Split(first, "\n")
	if len(displayLines) < len(selectable) {
		t.Fatalf("display has %d lines, fewer than %d selectable", len(displayLines), len(selectable))
	}
	second, _ := m.renderSelectableAssistantRow(0, row, 80, 5)
	if first != second {
		t.Fatal("expected stable cached render for an inactive selection")
	}
}
