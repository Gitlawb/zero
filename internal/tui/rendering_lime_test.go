package tui

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/tools"
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// plainRender strips styling so assertions run against text, not styled
// bytes. (Without a TTY lipgloss already renders plain; this keeps the tests
// honest either way.)
func plainRender(t *testing.T, rendered string) string {
	t.Helper()
	return ansiPattern.ReplaceAllString(rendered, "")
}

func limeTestModel() model {
	return newModel(context.Background(), Options{ProviderName: "anthropic", ModelName: "claude-sonnet-4.5"})
}

func TestUserRowRendersPromptGutter(t *testing.T) {
	m := limeTestModel()
	row := transcriptRow{kind: rowUser, text: "add a --version flag"}
	got := plainRender(t, m.renderRow(row, 96, buildRowContext(nil)))
	if !strings.HasPrefix(got, "❯ add a --version flag") {
		t.Fatalf("user row = %q, want ❯-prefixed text", got)
	}
}

func TestInterimBlockShowsStreamingTextWithCursor(t *testing.T) {
	m := limeTestModel()
	m.pending = true
	m.streamingText = "I'll add a --version flag"
	got := plainRender(t, m.interimBlock(96))
	if !strings.Contains(got, "I'll add a --version flag") || !strings.Contains(got, "▌") {
		t.Fatalf("interim block = %q, want streamed text with trailing cursor", got)
	}

	// Before the first delta the block falls back to the liveness spinner.
	m.streamingText = ""
	if got := plainRender(t, m.interimBlock(96)); !strings.Contains(got, "working…") {
		t.Fatalf("empty interim block = %q, want working…", got)
	}
}

func TestFinalAnswerRendersRailAndDoneLine(t *testing.T) {
	m := limeTestModel()
	row := transcriptRow{
		kind:        rowAssistant,
		text:        "Done — the CLI now prints its version.",
		final:       true,
		turnTools:   2,
		turnElapsed: 8400 * time.Millisecond,
	}
	got := plainRender(t, m.renderRow(row, 96, buildRowContext(nil)))
	if !strings.Contains(got, "│ Done — the CLI now prints its version.") {
		t.Fatalf("final row = %q, want accent-rail gutter", got)
	}
	if !strings.Contains(got, "● done · 2 tools · 8.4s") {
		t.Fatalf("final row = %q, want done line with counters", got)
	}
}

func TestDoneLineOmitsMissingSegments(t *testing.T) {
	got := plainRender(t, doneLine(transcriptRow{final: true}, false))
	if got != "● done" {
		t.Fatalf("done line without counters = %q, want plain ● done", got)
	}
	if got := plainRender(t, doneLine(transcriptRow{final: true, turnTools: 1}, false)); !strings.Contains(got, "1 tool") || strings.Contains(got, "1 tools") {
		t.Fatalf("done line = %q, want singular tool noun", got)
	}
}

func TestInterimAssistantRowRendersAsProse(t *testing.T) {
	m := limeTestModel()
	row := transcriptRow{kind: rowAssistant, text: "No provider configured."}
	got := plainRender(t, m.renderRow(row, 96, buildRowContext(nil)))
	if strings.Contains(got, "│") || strings.Contains(got, "●") {
		t.Fatalf("non-final assistant row = %q, must not carry rail or done line", got)
	}
}

func TestErrorRowRendersTintedNoteAndErrorDoneLine(t *testing.T) {
	m := limeTestModel()
	row := transcriptRow{kind: rowError, text: "provider exploded", final: true, turnTools: 1}
	got := plainRender(t, m.renderRow(row, 60, buildRowContext(nil)))
	if !strings.Contains(got, "╭") || !strings.Contains(got, "provider exploded") {
		t.Fatalf("error row = %q, want bordered note", got)
	}
	if !strings.Contains(got, "● error · 1 tool") {
		t.Fatalf("error row = %q, want error done line", got)
	}
}

func TestSystemNoteRendersBordered(t *testing.T) {
	m := limeTestModel()
	row := transcriptRow{kind: rowSystem, text: "Mode set to ask."}
	got := plainRender(t, m.renderRow(row, 60, buildRowContext(nil)))
	if !strings.Contains(got, "╭") || !strings.Contains(got, "Mode set to ask.") {
		t.Fatalf("system row = %q, want bordered note with content unchanged", got)
	}
}

func TestRunningToolCardShowsHeadAndSpinnerSlot(t *testing.T) {
	m := limeTestModel()
	m.pending = true
	row := transcriptRow{kind: rowToolCall, id: "call_1", tool: "grep", detail: "internal/cli"}
	got := plainRender(t, m.renderRow(row, 80, buildRowContext(nil)))
	if !strings.Contains(got, "grep") || !strings.Contains(got, "internal/cli") {
		t.Fatalf("running card = %q, want tool name and target in head", got)
	}
	if !strings.Contains(got, "╭") || !strings.Contains(got, "╰") {
		t.Fatalf("running card = %q, want a bordered card", got)
	}
}

func TestResolvedToolCallCollapsesIntoResultCard(t *testing.T) {
	rows := []transcriptRow{
		{kind: rowToolCall, id: "call_1", tool: "read_file", detail: "README.md"},
		{kind: rowToolResult, id: "call_1", tool: "read_file", status: tools.StatusOK, detail: "File: README.md\n\n1: # Zero"},
	}
	rc := buildRowContext(rows)
	if !rc.skip(rows[0]) {
		t.Fatal("a tool call with a result must collapse into the result card")
	}
	if rc.skip(rows[1]) {
		t.Fatal("the result row itself must render")
	}
}

func TestDiffCardBodyRendersCountsNumbersAndCap(t *testing.T) {
	m := limeTestModel()
	diff := strings.Join([]string{
		"--- /dev/null",
		"+++ b/internal/cli/root.go",
		"@@ -0,0 +1,3 @@",
		"+package cli",
		"+",
		"+var Version = \"dev\"",
	}, "\n")
	row := transcriptRow{kind: rowToolResult, id: "call_1", tool: "edit_file", status: tools.StatusOK, detail: diff}
	got := plainRender(t, m.renderRow(row, 80, buildRowContext(nil)))
	for _, want := range []string{"internal/cli/root.go", "NEW FILE", "+3", "package cli", "   1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("diff card = %q, missing %q", got, want)
		}
	}

	// The 16-line cap keeps long diffs bounded.
	long := []string{"+++ b/big.go", "@@ -0,0 +1,40 @@"}
	for i := 0; i < 40; i++ {
		long = append(long, "+line")
	}
	row.detail = strings.Join(long, "\n")
	got = plainRender(t, m.renderRow(row, 80, buildRowContext(nil)))
	if !strings.Contains(got, "more lines") {
		t.Fatalf("long diff card should cap at %d lines with a trailer, got %q", cardBodyMaxLines, got)
	}
}

func TestReadCardBodyShowsGutterAndRange(t *testing.T) {
	m := limeTestModel()
	detail := "File: internal/agent/loop.go\n\n12: func Run() {\n13: }\n"
	row := transcriptRow{kind: rowToolResult, id: "call_1", tool: "read_file", status: tools.StatusOK, detail: detail}
	rc := buildRowContext([]transcriptRow{{kind: rowToolCall, id: "call_1", tool: "read_file", detail: "internal/agent/loop.go"}})
	got := plainRender(t, m.renderRow(row, 80, rc))
	for _, want := range []string{"read_file", "internal/agent/loop.go", "L12–L13", "func Run() {"} {
		if !strings.Contains(got, want) {
			t.Fatalf("read card = %q, missing %q", got, want)
		}
	}
}

func TestBashCardBodyShowsCommandOutputAndExit(t *testing.T) {
	m := limeTestModel()
	detail := "stdout:\nok build\nstderr:\nwarning: slow\nexit_code: 1"
	row := transcriptRow{kind: rowToolResult, id: "call_1", tool: "bash", status: tools.StatusError, detail: detail}
	rc := buildRowContext([]transcriptRow{{kind: rowToolCall, id: "call_1", tool: "bash", detail: "go build ./..."}})
	got := plainRender(t, m.renderRow(row, 80, rc))
	for _, want := range []string{"❯ go build ./...", "ok build", "warning: slow", "exit 1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("bash card = %q, missing %q", got, want)
		}
	}
	if strings.Contains(got, "stdout:") || strings.Contains(got, "exit_code:") {
		t.Fatalf("bash card = %q, must restyle section markers", got)
	}
}

func TestGrepCardBodyShowsLocationsAndMatchCount(t *testing.T) {
	m := limeTestModel()
	detail := "internal/cli/root.go:41: fs := flag.NewFlagSet\ninternal/cli/app.go:12: flag.Parse()"
	row := transcriptRow{kind: rowToolResult, id: "call_1", tool: "grep", status: tools.StatusOK, detail: detail}
	got := plainRender(t, m.renderRow(row, 90, buildRowContext(nil)))
	for _, want := range []string{"internal/cli/root.go:41", "2 matches"} {
		if !strings.Contains(got, want) {
			t.Fatalf("grep card = %q, missing %q", got, want)
		}
	}
}

func TestToolCardMarksAutoApprovedCalls(t *testing.T) {
	m := limeTestModel()
	rows := []transcriptRow{
		{kind: rowToolCall, id: "call_1", tool: "edit_file", detail: "main.go"},
		{kind: rowPermission, id: "call_1", permission: &agent.PermissionEvent{
			ToolCallID: "call_1", ToolName: "edit_file", Action: agent.PermissionActionAllow, GrantMatched: true,
		}},
		{kind: rowToolResult, id: "call_1", tool: "edit_file", status: tools.StatusOK, detail: "ok"},
	}
	rc := buildRowContext(rows)
	got := plainRender(t, m.renderRow(rows[2], 80, rc))
	if !strings.Contains(got, "[auto]") {
		t.Fatalf("grant-approved card = %q, want [auto] tag", got)
	}

	// A prompted-then-allowed call was a manual decision: no auto tag.
	manual := []transcriptRow{
		{kind: rowToolCall, id: "call_2", tool: "bash", detail: "rm -rf ./tmp"},
		{kind: rowPermission, id: "call_2", permission: &agent.PermissionEvent{
			ToolCallID: "call_2", ToolName: "bash", Action: agent.PermissionActionPrompt,
		}},
		{kind: rowPermission, id: "call_2", permission: &agent.PermissionEvent{
			ToolCallID: "call_2", ToolName: "bash", Action: agent.PermissionActionAllow,
		}},
		{kind: rowToolResult, id: "call_2", tool: "bash", status: tools.StatusOK, detail: "ok"},
	}
	rcManual := buildRowContext(manual)
	if got := plainRender(t, m.renderRow(manual[3], 80, rcManual)); strings.Contains(got, "[auto]") {
		t.Fatalf("manually-approved card = %q, must not carry [auto]", got)
	}
}

func TestComposerLineTracksRunState(t *testing.T) {
	m := limeTestModel()
	m.input.SetValue("add a flag")
	if got := plainRender(t, m.composerLine(96)); !strings.Contains(got, "run ↵") {
		t.Fatalf("idle composer = %q, want run ↵ hint", got)
	}

	m.pending = true
	if got := plainRender(t, m.composerLine(96)); !strings.Contains(got, "esc stop") {
		t.Fatalf("pending composer = %q, want esc stop hint", got)
	}

	m.input.SetValue("")
	if got := plainRender(t, m.composerLine(96)); !strings.Contains(got, composerPlaceholderRunning) {
		t.Fatalf("pending empty composer = %q, want running placeholder", got)
	}
}

func TestStatusLineGroups(t *testing.T) {
	m := limeTestModel()
	got := plainRender(t, m.statusLine(110))
	for _, want := range []string{"● anthropic", "claude-sonnet-4.5", "interactive", "⏵⏵ auto-approve"} {
		if !strings.Contains(got, want) {
			t.Fatalf("status line = %q, missing %q", got, want)
		}
	}
}

func TestTitleBarShowsBadgeModelAndContextWindow(t *testing.T) {
	m := limeTestModel()
	m.width = 120
	got := plainRender(t, m.titleBar(120))
	for _, want := range []string{" 0 ", "zero", "anthropic/claude-sonnet-4.5", "200K"} {
		if !strings.Contains(got, want) {
			t.Fatalf("title bar = %q, missing %q", got, want)
		}
	}
}

func TestFormatContextWindow(t *testing.T) {
	cases := map[int]string{200000: "200K", 1000000: "1M", 128000: "128K", 0: ""}
	for window, want := range cases {
		if got := formatContextWindow(window); got != want {
			t.Fatalf("formatContextWindow(%d) = %q, want %q", window, got, want)
		}
	}
}
