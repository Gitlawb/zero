package acp

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/tools"
)

func TestAgentMessageAndThoughtChunks(t *testing.T) {
	m := agentMessageChunk("hello")
	if m.SessionUpdate != UpdateAgentMessageChunk || m.Content.Type != "text" || m.Content.Text != "hello" {
		t.Fatalf("unexpected message chunk: %+v", m)
	}
	th := agentThoughtChunk("thinking")
	if th.SessionUpdate != UpdateAgentThoughtChunk || th.Content.Text != "thinking" {
		t.Fatalf("unexpected thought chunk: %+v", th)
	}
}

func TestToolKindFor(t *testing.T) {
	cases := map[string]string{
		"read_file":      ToolKindRead,
		"list_directory": ToolKindRead,
		"grep":           ToolKindSearch,
		"glob":           ToolKindSearch,
		"edit_file":      ToolKindEdit,
		"apply_patch":    ToolKindEdit,
		"bash":           ToolKindExecute,
		"exec_command":   ToolKindExecute,
		"web_fetch":      ToolKindFetch,
		"update_plan":    ToolKindThink,
		"some_mcp_tool":  ToolKindOther,
	}
	for name, want := range cases {
		if got := toolKindFor(name); got != want {
			t.Errorf("toolKindFor(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestToolTitleAndHint(t *testing.T) {
	if got := toolTitle("read_file", `{"path":"src/main.go"}`); got != "read_file src/main.go" {
		t.Errorf("title = %q", got)
	}
	if got := toolTitle("bash", `{"command":"go test ./..."}`); got != "bash go test ./..." {
		t.Errorf("title = %q", got)
	}
	if got := toolTitle("mystery", `not json`); got != "mystery" {
		t.Errorf("malformed args should yield bare name, got %q", got)
	}
	if got := toolTitle("noargs", ``); got != "noargs" {
		t.Errorf("empty args should yield bare name, got %q", got)
	}
}

func TestToolCallStart(t *testing.T) {
	upd := toolCallStart(agent.ToolCall{ID: "tc1", Name: "read_file", Arguments: `{"path":"a.go"}`})
	if upd.SessionUpdate != UpdateToolCall {
		t.Fatalf("sessionUpdate = %q", upd.SessionUpdate)
	}
	if upd.ToolCallID != "tc1" || upd.Status != ToolStatusInProgress || upd.Kind != ToolKindRead {
		t.Fatalf("unexpected start: %+v", upd)
	}
	if string(upd.RawInput) != `{"path":"a.go"}` {
		t.Fatalf("rawInput = %s", upd.RawInput)
	}
	// Malformed args must not produce invalid JSON on the wire.
	if got := toolCallStart(agent.ToolCall{ID: "x", Name: "bash", Arguments: "broken"}); got.RawInput != nil {
		t.Fatalf("malformed args should drop rawInput, got %s", got.RawInput)
	}
}

func TestToolCallResult(t *testing.T) {
	ok := toolCallResult(agent.ToolResult{
		ToolCallID:   "tc1",
		Name:         "edit_file",
		Status:       tools.StatusOK,
		Output:       "applied\n",
		ChangedFiles: []string{"a.go", ""},
	})
	if ok.SessionUpdate != UpdateToolCallUpdate || ok.Status != ToolStatusCompleted {
		t.Fatalf("unexpected ok result: %+v", ok)
	}
	if len(ok.Content) != 1 || ok.Content[0].Type != "content" || ok.Content[0].Content.Text != "applied" {
		t.Fatalf("unexpected content: %+v", ok.Content)
	}
	if len(ok.Locations) != 1 || ok.Locations[0].Path != "a.go" {
		t.Fatalf("blank changed files should be dropped, got %+v", ok.Locations)
	}

	failed := toolCallResult(agent.ToolResult{ToolCallID: "tc2", Status: tools.StatusError, Output: "boom"})
	if failed.Status != ToolStatusFailed {
		t.Fatalf("error result should be failed, got %q", failed.Status)
	}
}

func TestPlanUpdateAndStatus(t *testing.T) {
	upd := planUpdate([]tools.PlanItem{
		{Content: "step a", Status: "completed"},
		{Content: "step b", Status: "in_progress"},
		{Content: "step c", Status: "failed"},
		{Content: "step d", Status: "weird"},
	})
	if upd.SessionUpdate != UpdatePlan || len(upd.Entries) != 4 {
		t.Fatalf("unexpected plan: %+v", upd)
	}
	want := []string{PlanStatusCompleted, PlanStatusInProgress, PlanStatusCompleted, PlanStatusPending}
	for i, w := range want {
		if upd.Entries[i].Status != w {
			t.Errorf("entry %d status = %q, want %q", i, upd.Entries[i].Status, w)
		}
		if upd.Entries[i].Priority != PlanPriorityMedium {
			t.Errorf("entry %d priority = %q", i, upd.Entries[i].Priority)
		}
	}
}

func TestPromptText(t *testing.T) {
	got := promptText([]ContentBlock{
		TextBlock("hello "),
		ImageBlock("base64", "image/png"),
		TextBlock("world"),
	})
	if got != "hello world" {
		t.Fatalf("promptText = %q", got)
	}
}

func TestToolTitleTruncateHintRuneSafe(t *testing.T) {
	// A 61-character string containing multi-byte UTF-8 runes (emojis / CJK characters).
	// We want to verify that it is truncated without cutting any runes or producing invalid UTF-8.
	longPath := "📁/项目/非常长的路径名称/测试/🚀/emoji-and-cjk-characters-which-are-very-long-and-exceed-sixty-characters"

	// Create JSON args for read_file
	rawArgs := `{"path":"` + longPath + `"}`
	got := toolTitle("read_file", rawArgs)

	expectedPrefix := "read_file "
	if !strings.HasPrefix(got, expectedPrefix) {
		t.Fatalf("expected title to start with %q, got %q", expectedPrefix, got)
	}

	hint := strings.TrimPrefix(got, expectedPrefix)
	// Hint should end with the ellipsis character
	if !strings.HasSuffix(hint, "…") {
		t.Fatalf("expected truncated hint to end with ellipsis, got %q", hint)
	}

	// Check that we don't have invalid UTF-8 runes
	if !utf8.ValidString(hint) {
		t.Fatalf("truncated hint is not a valid UTF-8 string: %q", hint)
	}

	// The rune count of the hint (excluding ellipsis) should be exactly 60
	runes := []rune(strings.TrimSuffix(hint, "…"))
	if len(runes) != 60 {
		t.Fatalf("expected exactly 60 runes before ellipsis, got %d (hint: %q)", len(runes), hint)
	}
}
