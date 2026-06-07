package zeroline

import (
	"strings"
	"testing"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// forceColor makes lipgloss emit ANSI escapes regardless of the test TTY so we
// can assert that styled output is actually colored.
func forceColor(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

func TestRenderMarkdownStripsSyntax(t *testing.T) {
	p := Resolve(0, true)
	out := renderMarkdown("**bold** and `code`", p, 0, true, 60)
	if strings.TrimSpace(out) == "" {
		t.Fatal("renderMarkdown returned empty output")
	}
	if strings.Contains(out, "**") {
		t.Errorf("markdown markers leaked into output: %q", stripANSI(out))
	}
	// the words themselves must survive the render
	plain := stripANSI(out)
	for _, w := range []string{"bold", "code"} {
		if !strings.Contains(plain, w) {
			t.Errorf("rendered markdown missing %q: %q", w, plain)
		}
	}
	// no trailing blank lines (we Trim them so transcript spacing stays tight)
	if strings.HasSuffix(out, "\n") || strings.HasPrefix(out, "\n") {
		t.Errorf("renderMarkdown left surrounding blank lines: %q", out)
	}
}

func TestRenderMarkdownHighlightsCodeFence(t *testing.T) {
	p := Resolve(1, true)
	src := "Here is code:\n\n```go\nfunc main() {}\n```\n"
	out := renderMarkdown(src, p, 1, true, 70)
	plain := stripANSI(out)
	if !strings.Contains(plain, "func main()") {
		t.Errorf("fenced code body missing from render: %q", plain)
	}
	if strings.Contains(plain, "```") {
		t.Errorf("code fence markers leaked into output: %q", plain)
	}
}

func TestMarkdownRendererCachedPerKey(t *testing.T) {
	p := Resolve(2, true)
	r1 := markdownRenderer(p, 2, true, 80)
	r2 := markdownRenderer(p, 2, true, 80)
	if r1 != r2 {
		t.Error("expected same cached renderer for identical key")
	}
	r3 := markdownRenderer(p, 2, true, 81) // width change -> new renderer
	if r1 == r3 {
		t.Error("width change should produce a distinct renderer")
	}
	r4 := markdownRenderer(p, 3, true, 80) // variant change -> new renderer
	if r1 == r4 {
		t.Error("variant change should produce a distinct renderer")
	}
}

func TestMarkdownRendererCacheBounded(t *testing.T) {
	// A resize drag mints a fresh width key on every frame, so the renderer cache
	// (the expensive goldmark+chroma objects) must be bounded just like the output
	// cache — otherwise it grows without limit.
	p := Resolve(0, true)

	mdMu.Lock()
	mdCache = map[mdKey]*glamour.TermRenderer{}
	mdMu.Unlock()

	for w := 1; w <= mdCacheMax*3; w++ {
		markdownRenderer(p, 0, true, w)
	}

	mdMu.Lock()
	n := len(mdCache)
	mdMu.Unlock()
	if n > mdCacheMax {
		t.Errorf("mdCache grew past bound: %d entries, want <= %d", n, mdCacheMax)
	}
}

func TestColorizeDiffColorsAddsAndDels(t *testing.T) {
	forceColor(t)
	p := Resolve(0, true)
	diff := "@@ -1,2 +1,2 @@\n context line\n-removed line\n+added line\n"
	out := colorizeDiff(diff, p)

	// content survives
	plain := stripANSI(out)
	for _, w := range []string{"added line", "removed line", "context line"} {
		if !strings.Contains(plain, w) {
			t.Errorf("diff output missing %q: %q", w, plain)
		}
	}
	// a subtle left gutter is present
	if !strings.Contains(plain, "│") {
		t.Errorf("diff output missing left gutter: %q", plain)
	}

	// adds and dels are colored DISTINCTLY: the addition is rendered in the
	// theme Green and the deletion in the theme Red. The diff line keeps its
	// leading +/- marker, so we assert against the marked text.
	green := lipgloss.NewStyle().Foreground(p.Green).Render("+added line")
	red := lipgloss.NewStyle().Foreground(p.Red).Render("-removed line")
	if !strings.Contains(out, green) {
		t.Errorf("added line not colored with theme Green")
	}
	if !strings.Contains(out, red) {
		t.Errorf("removed line not colored with theme Red")
	}
	if string(p.Green) == string(p.Red) {
		t.Fatal("theme 0 unexpectedly uses the same color for green/red")
	}
}

func TestColorizeDiffCapsLongDiffs(t *testing.T) {
	p := Resolve(0, true)
	var b strings.Builder
	b.WriteString("@@ -1,100 +1,100 @@\n")
	for i := 0; i < 100; i++ {
		b.WriteString("+line\n")
	}
	out := stripANSI(colorizeDiff(b.String(), p))
	if !strings.Contains(out, "more lines") {
		t.Errorf("long diff not capped with a footer: %q", out)
	}
	// the cap means far fewer than 101 rendered lines
	if n := strings.Count(out, "\n") + 1; n > diffMaxLines+2 {
		t.Errorf("diff not capped: %d lines rendered", n)
	}
}

func TestLooksLikeDiff(t *testing.T) {
	cases := map[string]bool{
		"@@ -1 +1 @@\n-a\n+b":        true,
		"--- a\n+++ b\n-x\n+y":       true,
		"+just an addition":         true,
		"plain prose with no diff":  false,
		"wrote 12 lines to file.go": false,
	}
	for in, want := range cases {
		if got := looksLikeDiff(in); got != want {
			t.Errorf("looksLikeDiff(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestAssistantRowRendersMarkdown(t *testing.T) {
	d := ChatData{
		Variant: 0, Dark: true, Width: 100, Height: 40,
		Rows: []Row{
			{Kind: "assistant", Text: "Use **bold** and a list:\n\n- one\n- two"},
		},
	}
	out := stripANSI(RenderChat(d))
	if strings.Contains(out, "**bold**") {
		t.Errorf("completed assistant markdown not rendered (raw markers present): %q", out)
	}
	if !strings.Contains(out, "bold") {
		t.Errorf("assistant text missing from render: %q", out)
	}
}

func TestStreamingAssistantStaysPlain(t *testing.T) {
	d := ChatData{
		Variant: 0, Dark: true, Width: 100, Height: 40,
		Stream: "partial **incomplete",
	}
	out := stripANSI(RenderChat(d))
	// Streaming text is shown verbatim (not run through glamour): the raw markdown
	// markers must survive. Glamour would strip/render "**" into a bold span, so
	// asserting the literal "**incomplete" is preserved proves the verbatim path.
	if !strings.Contains(out, "partial **incomplete") {
		t.Errorf("streaming text not verbatim (markdown markers stripped?): %q", out)
	}
}
