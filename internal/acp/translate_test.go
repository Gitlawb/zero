package acp

import (
	"encoding/json"
	"path/filepath"
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
	path := filepath.Join(t.TempDir(), "a.go")
	ok := toolCallResult(agent.ToolResult{
		ToolCallID:   "tc1",
		Name:         "edit_file",
		Status:       tools.StatusOK,
		Output:       "applied\n",
		ChangedFiles: []string{"a.go", ""},
		FileDiffs:    []tools.FileDiff{{Path: path, OldExists: true, NewExists: true, OldText: "before\n", NewText: "after\n"}},
	})
	if ok.SessionUpdate != UpdateToolCallUpdate || ok.Status != ToolStatusCompleted {
		t.Fatalf("unexpected ok result: %+v", ok)
	}
	if len(ok.Content) != 2 || ok.Content[0].Type != "content" || ok.Content[0].Content.Text != "applied" {
		t.Fatalf("unexpected content: %+v", ok.Content)
	}
	if diff := ok.Content[1]; diff.Type != "diff" || diff.Path != path || diff.OldText == nil || *diff.OldText != "before\n" || diff.NewText == nil || *diff.NewText != "after\n" {
		t.Fatalf("unexpected diff content: %+v", diff)
	}
	if len(ok.Locations) != 2 || ok.Locations[0].Path != path || ok.Locations[1].Path != "a.go" {
		t.Fatalf("unproven absolute/relative aliases must both remain visible, got %+v", ok.Locations)
	}

	failed := toolCallResult(agent.ToolResult{ToolCallID: "tc2", Status: tools.StatusError, Output: "boom"})
	if failed.Status != ToolStatusFailed {
		t.Fatalf("error result should be failed, got %q", failed.Status)
	}
}

func TestToolCallDiffJSONPreservesEmptyFilesWithoutClaimingDeletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.txt")
	content := appendToolResultDiffs(nil, []tools.FileDiff{
		{Path: path, OldExists: false, NewExists: true, NewText: ""},
		{Path: path, OldExists: true, NewExists: true, OldText: "before", NewText: ""},
		{Path: path, OldExists: true, NewExists: false, OldText: "before"},
	})
	if len(content) != 2 {
		t.Fatalf("diff content = %#v", content)
	}
	for index, diff := range content {
		encoded, err := json.Marshal(diff)
		if err != nil {
			t.Fatal(err)
		}
		var wire map[string]any
		if err := json.Unmarshal(encoded, &wire); err != nil {
			t.Fatal(err)
		}
		if wire["path"] != path || wire["newText"] != "" {
			t.Fatalf("wire diff %d = %s", index, encoded)
		}
		if index == 0 && wire["oldText"] != nil {
			t.Fatalf("create oldText = %#v, want null", wire["oldText"])
		}
		if index == 1 && wire["oldText"] != "before" {
			t.Fatalf("update oldText = %#v, want before", wire["oldText"])
		}
	}
}

func TestToolResultLocationsPreserveDistinctPathIdentities(t *testing.T) {
	root := t.TempDir()
	rootPath := filepath.Join(root, "a.go")
	nestedPath := filepath.Join(root, "sub", "a.go")
	diff := func(path string) tools.FileDiff {
		return tools.FileDiff{Path: path, OldExists: true, NewExists: true, OldText: "before", NewText: "after"}
	}
	for _, tc := range []struct {
		name  string
		diffs []tools.FileDiff
		want  []string
	}{
		{name: "both rich", diffs: []tools.FileDiff{diff(rootPath), diff(nestedPath)}, want: []string{rootPath, nestedPath, "a.go", filepath.Join("sub", "a.go")}},
		{name: "root rich", diffs: []tools.FileDiff{diff(rootPath)}, want: []string{rootPath, "a.go", filepath.Join("sub", "a.go")}},
		{name: "nested rich", diffs: []tools.FileDiff{diff(nestedPath)}, want: []string{nestedPath, "a.go", filepath.Join("sub", "a.go")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			locations := toolResultLocations(agent.ToolResult{
				ChangedFiles: []string{"a.go", filepath.Join("sub", "a.go")},
				FileDiffs:    tc.diffs,
			})
			if len(locations) != len(tc.want) {
				t.Fatalf("locations = %#v, want %#v", locations, tc.want)
			}
			for index := range tc.want {
				if locations[index].Path != tc.want[index] {
					t.Fatalf("locations = %#v, want %#v", locations, tc.want)
				}
			}
		})
	}
}

func TestToolCallResultPreservesWhitespaceInFilePaths(t *testing.T) {
	relativePath := " report.txt "
	absolutePath := filepath.Join(t.TempDir(), relativePath)
	update := toolCallResult(agent.ToolResult{
		ChangedFiles: []string{relativePath},
		FileDiffs: []tools.FileDiff{{
			Path: absolutePath, OldExists: true, NewExists: true, OldText: "before", NewText: "after",
		}},
	})
	if len(update.Content) != 1 || update.Content[0].Path != absolutePath {
		t.Fatalf("diff content path = %#v, want %q", update.Content, absolutePath)
	}
	if len(update.Locations) != 2 || update.Locations[0].Path != absolutePath || update.Locations[1].Path != relativePath {
		t.Fatalf("locations = %#v, want exact paths %q and %q", update.Locations, absolutePath, relativePath)
	}
}

func TestToolResultLocationsDeduplicateOnlyExactPaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.go")
	locations := toolResultLocations(agent.ToolResult{
		ChangedFiles: []string{path, path},
		FileDiffs:    []tools.FileDiff{{Path: path, OldExists: true, NewExists: true, OldText: "before", NewText: "after"}},
	})
	if len(locations) != 1 || locations[0].Path != path {
		t.Fatalf("exact duplicate locations = %#v", locations)
	}
}

func TestDeletedFileKeepsPathOnlyLocation(t *testing.T) {
	relativePath := "deleted.go"
	absolutePath := filepath.Join(t.TempDir(), relativePath)
	update := toolCallResult(agent.ToolResult{
		ChangedFiles: []string{relativePath},
		FileDiffs: []tools.FileDiff{{
			Path: absolutePath, OldExists: true, NewExists: false, OldText: "before",
		}},
	})
	if len(update.Content) != 0 {
		t.Fatalf("deleted file must not emit an ambiguous ACP diff: %#v", update.Content)
	}
	if len(update.Locations) != 2 || update.Locations[0].Path != absolutePath || update.Locations[1].Path != relativePath {
		t.Fatalf("deleted file locations = %#v", update.Locations)
	}
}

func TestToolCallResultEmitsOnlyRedactedFileDiffs(t *testing.T) {
	secret := "sk-proj-abcdefghijklmnopqrstuvwxyz"
	path := filepath.Join(t.TempDir(), "secret.txt")
	scrubbed := tools.ScrubResultSecrets(tools.Result{FileDiffs: []tools.FileDiff{{
		Path: path, OldExists: true, NewExists: true, OldText: "token=" + secret, NewText: "safe",
	}}})
	update := toolCallResult(agent.ToolResult{ToolCallID: "call", Status: tools.StatusError, FileDiffs: scrubbed.FileDiffs})
	if len(update.Content) != 1 || update.Content[0].OldText == nil || strings.Contains(*update.Content[0].OldText, secret) {
		t.Fatalf("ACP content leaked unredacted diff: %#v", update.Content)
	}
}

func TestToolCallResultOmitsDefaultIgnorableSplitSecretsOnEitherSide(t *testing.T) {
	secret := "sk-ant-api03-AAAABBBBCCCCDDDDEEEEFFFFGGGG"
	for name, separator := range map[string]string{
		"combining grapheme joiner": "\u034f",
		"variation selector":        "\ufe0f",
	} {
		for _, side := range []string{"old", "new"} {
			t.Run(name+" "+side, func(t *testing.T) {
				obfuscated := secret[:20] + separator + secret[20:]
				diff := tools.FileDiff{
					Path: filepath.Join(t.TempDir(), "secret.txt"), OldExists: true, NewExists: true,
					OldText: "safe old", NewText: "safe new",
				}
				if side == "old" {
					diff.OldText = obfuscated
				} else {
					diff.NewText = obfuscated
				}
				scrubbed := tools.ScrubResultSecrets(tools.Result{FileDiffs: []tools.FileDiff{diff}})
				if !scrubbed.Redacted || len(scrubbed.FileDiffs) != 0 {
					t.Fatalf("registry boundary retained an obfuscated secret: %#v", scrubbed)
				}
				update := toolCallResult(agent.ToolResult{ToolCallID: "call", Status: tools.StatusOK, FileDiffs: scrubbed.FileDiffs})
				if len(update.Content) != 0 {
					t.Fatalf("ACP content retained an obfuscated secret: %#v", update.Content)
				}
			})
		}
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
