package tui

import (
	"strings"
	"testing"
)

func TestDecodeStreamingJSONString(t *testing.T) {
	// Complete path + unterminated streaming content with escapes.
	args := `{"path":"ecommerce/frontend/index.html","content":"<!DOCTYPE html>\n<html>\n  <body class=\"x\">`
	if got := streamingFilePath(args); got != "ecommerce/frontend/index.html" {
		t.Errorf("path = %q", got)
	}
	content, ok := streamingFileContent(args)
	if !ok {
		t.Fatal("expected content")
	}
	want := "<!DOCTYPE html>\n<html>\n  <body class=\"x\">"
	if content != want {
		t.Errorf("content = %q, want %q", content, want)
	}
	// A dangling backslash at the stream edge is dropped, not panicked on.
	if c, _ := streamingFileContent(`{"content":"abc\`); c != "abc" {
		t.Errorf("dangling escape: %q", c)
	}
	// Missing key.
	if _, ok := streamingFileContent(`{"path":"x"}`); ok {
		t.Error("no content key should be (false)")
	}
}

func TestStreamingToolCallView(t *testing.T) {
	// No active call → empty.
	if (model{}).streamingToolCallView(80) != "" {
		t.Error("inactive should render nothing")
	}
	// Non-file tool → empty.
	if (model{streamCallID: "1", streamCallName: "bash", streamCallArgs: `{"command":"ls"}`}).streamingToolCallView(80) != "" {
		t.Error("non-file tool should render nothing")
	}
	// Active write_file → shows path, line count, and a content tail.
	m := model{
		streamCallID:   "1",
		streamCallName: "write_file",
		streamCallArgs: `{"path":"a/b.go","content":"package main\n\nfunc main() {}\n"}`,
	}
	out := m.streamingToolCallView(80)
	for _, want := range []string{"write_file", "a/b.go", "lines", "func main()"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q:\n%s", want, out)
		}
	}
}

func TestDecodeStreamingTolerantOfWhitespace(t *testing.T) {
	// kimi-style spacing after the colon (and around it) must parse like compact JSON.
	for _, args := range []string{
		`{"path":"a.go","content":"line1\nline2"}`,
		`{"path": "a.go", "content": "line1\nline2"}`,
		`{ "path" : "a.go" , "content" : "line1\nline2" }`,
		`{"path":"a.go",` + "\n" + `  "content": "line1\nline2"}`,
	} {
		if got := streamingFilePath(args); got != "a.go" {
			t.Errorf("path from %q = %q, want a.go", args, got)
		}
		c, ok := streamingFileContent(args)
		if !ok || c != "line1\nline2" {
			t.Errorf("content from %q = %q ok=%v", args, c, ok)
		}
	}
}

func TestStreamingViewShowsProgressBeforeContent(t *testing.T) {
	// Args streaming but content not reached yet → show a byte count, not blank.
	m := model{streamCallID: "1", streamCallName: "write_file", streamCallArgs: `{"path": "website/css/styles.css", "content": "/* lo`}
	out := m.streamingToolCallView(80)
	if !strings.Contains(out, "website/css/styles.css") {
		t.Errorf("path should show with kimi-style spacing: %q", out)
	}
}

func TestStreamingFileContentEditFileKey(t *testing.T) {
	// edit_file's canonical replacement arg is new_string — the live preview must
	// extract it (regression for M13: edit preview showed nothing).
	args := `{"path":"a.go","old_string":"x","new_string":"package main\nfunc main(){}"}`
	c, ok := streamingFileContent(args)
	if !ok || c != "package main\nfunc main(){}" {
		t.Errorf("new_string content = %q ok=%v", c, ok)
	}
	if p := streamingFilePath(args); p != "a.go" {
		t.Errorf("path = %q", p)
	}
}

func TestStyleStreamingNewContentNeverRed(t *testing.T) {
	// L8: a brand-new file line beginning with "-" (e.g. CSS "-webkit-…") must NOT
	// be colored as a diff removal. With newContent=true it renders green; the
	// red/green diff branches apply only to apply_patch (newContent=false).
	green := zeroTheme.green.Render("-webkit-box-shadow: none;")
	got := styleStreamingCodeLine("-webkit-box-shadow: none;", true)
	if got != green {
		t.Errorf("new-file '-' line should render green, got %q want %q", got, green)
	}
	// And for a real patch (newContent=false) a '-' line IS a red removal.
	red := zeroTheme.red.Render("-old line")
	if got := styleStreamingCodeLine("-old line", false); got != red {
		t.Errorf("patch '-' line should render red, got %q", got)
	}
}
