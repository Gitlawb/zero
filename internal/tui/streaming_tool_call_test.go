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
	content, ok := decodeStreamingJSONString(args, "content")
	if !ok {
		t.Fatal("expected content")
	}
	want := "<!DOCTYPE html>\n<html>\n  <body class=\"x\">"
	if content != want {
		t.Errorf("content = %q, want %q", content, want)
	}
	// A dangling backslash at the stream edge is dropped, not panicked on.
	if c, _ := decodeStreamingJSONString(`{"content":"abc\`, "content"); c != "abc" {
		t.Errorf("dangling escape: %q", c)
	}
	// Missing key.
	if _, ok := decodeStreamingJSONString(`{"path":"x"}`, "content"); ok {
		t.Error("no content key should be (false)")
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
		c, ok := decodeStreamingJSONString(args, "content")
		if !ok || c != "line1\nline2" {
			t.Errorf("content from %q = %q ok=%v", args, c, ok)
		}
	}
}

func viewModel(name, args string) model {
	dec := newStreamingDecoder()
	dec.feed(args)
	return model{streamCallID: "1", streamCallName: name, streamCallDecoder: dec}
}

func TestStreamingToolCallView(t *testing.T) {
	// No active call → empty.
	if (model{}).streamingToolCallView(80) != "" {
		t.Error("inactive should render nothing")
	}
	// Non-file tool → empty.
	if viewModel("bash", `{"command":"ls"}`).streamingToolCallView(80) != "" {
		t.Error("non-file tool should render nothing")
	}
	// Active write_file → shows path and live count, but buffers the code body
	// until the final tool result lands.
	out := viewModel("write_file", `{"path":"a/b.go","content":"package main\n\nfunc main() {}\n"}`).streamingToolCallView(80)
	for _, want := range []string{zeroTheme.diffAdd.Render("+3"), zeroTheme.diffDel.Render("-0")} {
		if !strings.Contains(out, want) {
			t.Errorf("live count tag should color additions/deletions, missing styled %q in:\n%s", want, out)
		}
	}
	plain := plainRender(t, out)
	for _, want := range []string{"Adding", "a/b.go", "(+3 -0)"} {
		if !strings.Contains(plain, want) {
			t.Errorf("view missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(plain, "package main") || strings.Contains(plain, "func main()") {
		t.Errorf("live write preview should buffer code body until completion:\n%s", out)
	}
	if strings.Contains(out, "write_file") {
		t.Errorf("live preview must not expose raw tool name:\n%s", out)
	}
}

func TestStreamingToolCallViewShowsLatePath(t *testing.T) {
	dec := newStreamingDecoder()
	feedChunks(dec, `{"content":"from datetime import datetime\n`, `print(datetime.now())",`, `"path":"time_test.py"}`)
	m := model{streamCallID: "1", streamCallName: "write_file", streamCallDecoder: dec}
	out := plainRender(t, m.streamingToolCallView(80))
	for _, want := range []string{"Adding", "time_test.py", "(+2 -0)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("live write header = %q, missing %q", out, want)
		}
	}
}

func TestStreamingViewShowsProgressBeforeContent(t *testing.T) {
	// Path known but the content field hasn't arrived yet → show the path + a live
	// byte count, never blank.
	out := viewModel("write_file", `{"path": "website/css/styles.css"`).streamingToolCallView(80)
	if !strings.Contains(out, "website/css/styles.css") {
		t.Errorf("path should show with kimi-style spacing: %q", out)
	}
	if !strings.Contains(out, "KB") {
		t.Errorf("should show a receiving byte count before content: %q", out)
	}
}
