package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// wrapPlainText must preserve internal alignment (>=2-space runs) instead of
// collapsing it via strings.Fields, while leaving normal prose word-wrap intact.
func TestWrapPlainTextPreservesAlignedWhitespace(t *testing.T) {
	// Aligned columns that fit the measure survive verbatim.
	aligned := "name    status    time"
	if got := wrapPlainText(aligned, 40); len(got) != 1 || got[0] != aligned {
		t.Fatalf("aligned line not preserved: %q", got)
	}

	// Indented aligned line: leading indent AND internal alignment preserved.
	indented := "    col1    col2"
	if got := wrapPlainText(indented, 40); len(got) != 1 || got[0] != indented {
		t.Fatalf("indented aligned line not preserved: %q", got)
	}

	// Aligned line wider than the measure: verbatim split — concatenation equals
	// the original (nothing collapsed), every segment within the measure.
	wide := "aaaa    bbbb    cccc    dddd    eeee"
	got := wrapPlainText(wide, 16)
	if strings.Join(got, "") != wide {
		t.Fatalf("verbatim split altered content: %q -> %q", wide, got)
	}
	for _, l := range got {
		if lipgloss.Width(l) > 16 {
			t.Errorf("segment exceeds measure 16: %q (w=%d)", l, lipgloss.Width(l))
		}
	}

	// Normal prose (single spaces) keeps word-wrap: fits the measure, no introduced
	// double spaces, no mid-word breaks.
	prose := "the quick brown fox jumps over the lazy dog yet again today"
	for _, l := range wrapPlainText(prose, 20) {
		if lipgloss.Width(l) > 20 {
			t.Errorf("prose line exceeds measure: %q", l)
		}
		if strings.Contains(l, "  ") {
			t.Errorf("prose word-wrap introduced a double space: %q", l)
		}
	}

	// Explicit newlines and blank lines stay.
	multi := "a    b\n\nc    d"
	if got := wrapPlainText(multi, 40); len(got) != 3 || got[0] != "a    b" || got[1] != "" || got[2] != "c    d" {
		t.Fatalf("newlines/blank not preserved: %q", got)
	}
}
