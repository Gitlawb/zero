package tui

import (
	"strings"
	"testing"
)

// The live streaming render path (allowHighlight=false) must render code
// verbatim/plain — never tokenised — so the per-frame loop can't re-lex a
// growing block. Profile-independent: the plain path applies no styling, so the
// code appears as a contiguous substring (a highlighted block would not).
func TestStreamingCodeRendersPlain(t *testing.T) {
	md := "```go\nfunc main() {}\n```"
	out := strings.Join(renderAssistantMarkdownText(md, 80, 80, false), "\n")
	if !strings.Contains(out, "func main() {}") {
		t.Fatalf("interim render must keep code verbatim (no chroma), got:\n%s", out)
	}
}

// highlightCode must fall back (ok=false) on a missing/unknown language so the
// caller renders the block plain — never worse than today — and must preserve
// the line structure of a known language.
func TestHighlightCodeFallbackAndLineCount(t *testing.T) {
	if _, ok := highlightCode([]string{"x := 1"}, "", 80); ok {
		t.Error("empty language must fall back (ok=false) so the caller renders plain")
	}
	if _, ok := highlightCode([]string{"x"}, "definitely-not-a-language", 80); ok {
		t.Error("unknown language must fall back")
	}
	out, ok := highlightCode([]string{"package main", "func main() {}"}, "go", 80)
	if !ok {
		t.Fatal("go must have a lexer")
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 highlighted lines (structure preserved), got %d: %#v", len(out), out)
	}
}

// A line longer than the measure wraps at the token level (never loses content).
func TestHighlightCodeWraps(t *testing.T) {
	long := "x := 1 + 2 + 3 + 4 + 5 + 6 + 7 + 8 + 9 + 10 + 11 + 12"
	out, ok := highlightCode([]string{long}, "go", 20)
	if !ok {
		t.Fatal("go must have a lexer")
	}
	if len(out) < 2 {
		t.Fatalf("a long line should wrap into multiple rows, got %d", len(out))
	}
}
