package tui

import (
	"strings"
	"testing"
)

// The status glyph must LEAD the tool-card head row (✓/✗/spinner in the first
// cell after the rail), not be right-aligned to the far edge — so state reads at
// a glance. Guards the "easier-to-scan results" change.
func TestToolCardGlyphLeadsHeadRow(t *testing.T) {
	head := zeroTheme.toolName.Render("bash")
	glyph := zeroTheme.green.Render("✓")
	out := toolCard(head, glyph, nil, "", zeroTheme.line, 60)
	first := strings.SplitN(out, "\n", 2)[0]

	glyphIdx := strings.Index(first, "✓")
	nameIdx := strings.Index(first, "bash")
	if glyphIdx < 0 {
		t.Fatalf("head row missing status glyph: %q", first)
	}
	if nameIdx < 0 {
		t.Fatalf("head row missing tool name: %q", first)
	}
	if glyphIdx > nameIdx {
		t.Fatalf("status glyph must lead the tool name, got glyph@%d after name@%d: %q", glyphIdx, nameIdx, first)
	}
}
