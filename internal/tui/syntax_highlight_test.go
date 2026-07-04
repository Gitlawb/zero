package tui

import (
	"strings"
	"testing"
)

func TestStreamingCodeRendersHighlighted(t *testing.T) {
	m := model{
		streamingText: []byte("```go\nfunc main() {}\n```"),
		pending:       true,
	}
	out := m.interimBlock(80)
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("live code should be highlighted before commit, got:\n%s", out)
	}
	if !strings.Contains(plainRender(t, out), "func main() {}") {
		t.Fatalf("live code should keep the plain content, got:\n%s", out)
	}
}

func TestFinalBarePythonCodeRendersHighlighted(t *testing.T) {
	row := transcriptRow{
		kind:  rowAssistant,
		final: true,
		text: strings.Join([]string{
			"from datetime import datetime",
			"",
			"def print_current_time():",
			"    print(datetime.now().strftime(\"%Y-%m-%d %H:%M:%S\"))",
			"",
			"if __name__ == \"__main__\":",
			"    print_current_time()",
		}, "\n"),
	}
	out := renderAssistantRow(row, 90)
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("final bare Python code should be syntax-highlighted, got:\n%s", out)
	}
	plain := plainRender(t, out)
	if !strings.Contains(plain, "from datetime import datetime") ||
		!strings.Contains(plain, "def print_current_time():") ||
		!strings.Contains(plain, `if __name__ == "__main__":`) ||
		!strings.Contains(plain, "print_current_time()") {
		t.Fatalf("final bare Python code should keep content, got:\n%s", out)
	}
	for _, wantStyled := range []string{"from", "def", "if"} {
		if !strings.Contains(out, zeroTheme.accent.Render(wantStyled)) {
			t.Fatalf("final bare Python code should color keyword %q, got:\n%s", wantStyled, out)
		}
	}
}

func TestBareFencedCodeInfersLanguage(t *testing.T) {
	out := strings.Join(renderAssistantMarkdownText("```\nfrom datetime import datetime\nprint(datetime.now())\n```", 90, 90, true), "\n")
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("bare fenced Python code should infer language and highlight, got:\n%s", out)
	}
}

func TestPlainProseDoesNotTriggerBareCodeHighlight(t *testing.T) {
	out := strings.Join(renderAssistantMarkdownText("from here we continue with the explanation", 90, 90, true), "\n")
	if strings.Contains(out, "\x1b[") {
		t.Fatalf("plain prose should not be treated as bare code, got:\n%s", out)
	}
}

func TestBareCodeHighlightRequiresBlockSignal(t *testing.T) {
	out := strings.Join(renderAssistantMarkdownText("for these reasons:\nreturn later with a decision", 90, 90, true), "\n")
	if strings.Contains(out, "\x1b[") {
		t.Fatalf("ordinary prose should not be highlighted as bare code, got:\n%s", out)
	}
}

func TestStreamingMarkdownStablePrefixUsesRenderCache(t *testing.T) {
	defaultRenderCache.clear()
	text := "Here is the script:\n```go\nfunc main() {}\n```\nDone."
	_ = renderStreamingAssistantMarkdownText(text, 90, 90)
	before := defaultRenderCache.stats()
	_ = renderStreamingAssistantMarkdownText(text, 90, 90)
	after := defaultRenderCache.stats()
	if after.Hits <= before.Hits {
		t.Fatalf("streaming stable markdown should reuse highlighted cache, before=%+v after=%+v", before, after)
	}
}

func TestStreamingBuffersOpenFencedCodeBlock(t *testing.T) {
	open := model{
		streamingText: []byte("Here is the script:\n```python\nfrom datetime import datetime\nprint(datetime.now())"),
		pending:       true,
	}
	openOut := plainRender(t, open.interimBlock(90))
	if !strings.Contains(openOut, "Here is the script:") {
		t.Fatalf("streaming prose before code should remain visible, got:\n%s", openOut)
	}
	if strings.Contains(openOut, "datetime") || strings.Contains(openOut, "print(") {
		t.Fatalf("open fenced code should be buffered until the closing fence, got:\n%s", openOut)
	}

	closed := model{
		streamingText: []byte(string(open.streamingText) + "\n```"),
		pending:       true,
	}
	closedOut := closed.interimBlock(90)
	closedPlain := plainRender(t, closedOut)
	if !strings.Contains(closedPlain, "from datetime import datetime") || !strings.Contains(closedPlain, "print(datetime.now())") {
		t.Fatalf("closed fenced code should appear as one block, got:\n%s", closedOut)
	}
	if !strings.Contains(closedOut, "\x1b[") {
		t.Fatalf("closed streaming code should be highlighted, got:\n%s", closedOut)
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
