package tui

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/sandbox"
	"github.com/Gitlawb/zero/internal/tools"
)

// ansiPattern strips SGR styling and OSC sequences (hyperlinks) so
// assertions run against the visible text.
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m|\x1b\][^\a\x1b]*(?:\a|\x1b\\)`)

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
	// Mirrors the real read_file output shape: "<right-aligned N> | <text>".
	detail := "File: internal/agent/loop.go\n\n  12 | func Run() {\n  13 | }\n"
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

func TestToolBodyRendererRegistryCoversCoreCards(t *testing.T) {
	for _, name := range []string{"read_file", "bash", "grep", "unknown_tool"} {
		if toolBodyRendererFor(name) == nil {
			t.Fatalf("expected renderer for %s", name)
		}
	}
}

func TestBashCardBodyShowsShellIssueHint(t *testing.T) {
	m := limeTestModel()
	detail := strings.Join([]string{
		"stderr:",
		"The syntax of the command is incorrect.",
		"exit_code: 1",
		"[zero] shell issue: Windows cmd.exe rejected the command syntax.",
		"Suggestion: Use the cwd argument instead of cd.",
	}, "\n")
	row := transcriptRow{kind: rowToolResult, id: "call_1", tool: "bash", status: tools.StatusError, detail: detail}
	rc := buildRowContext([]transcriptRow{{kind: rowToolCall, id: "call_1", tool: "bash", detail: "cd /d/tmp/zero-pr-158 && ls -la"}})
	got := plainRender(t, m.renderRow(row, 96, rc))
	for _, want := range []string{"shell issue", "Windows cmd.exe", "Suggestion:", "cwd"} {
		if !strings.Contains(got, want) {
			t.Fatalf("bash shell issue card = %q, missing %q", got, want)
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
	if got := plainRender(t, m.composerLine(96)); strings.Contains(got, "run ↵") {
		t.Fatalf("idle composer = %q, should not show run hint", got)
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

func TestComposerLineShowsRequiredCommandArgumentHint(t *testing.T) {
	m := limeTestModel()
	m.input.Width = 40
	m.input.SetValue("/spec")
	m.input.CursorEnd()
	if got := plainRender(t, m.composerLine(96)); !strings.Contains(got, "/spec [task]") {
		t.Fatalf("composer line = %q, want /spec argument hint", got)
	}

	m.input.SetValue("/spec ")
	m.input.CursorEnd()
	if got := plainRender(t, m.composerLine(96)); !strings.Contains(got, "/spec [task]") || strings.Contains(got, "/spec  [task]") {
		t.Fatalf("composer line = %q, want /spec argument hint", got)
	}

	m.input.SetValue("/find ")
	m.input.CursorEnd()
	if got := plainRender(t, m.composerLine(96)); !strings.Contains(got, "/find") || !strings.Contains(got, "[query]") {
		t.Fatalf("composer line = %q, want /find query hint", got)
	}

	m.input.SetValue("/spec fix this")
	m.input.CursorEnd()
	if got := plainRender(t, m.composerLine(96)); strings.Contains(got, "[task]") {
		t.Fatalf("composer line = %q, should hide hint once an argument is present", got)
	}

	m.input.SetValue("/model ")
	m.input.CursorEnd()
	if got := plainRender(t, m.composerLine(96)); strings.Contains(got, "[list|id]") {
		t.Fatalf("composer line = %q, should not hint optional arguments", got)
	}
}

func TestComposerBoxFramesInputAndBottomModelModeLabel(t *testing.T) {
	m := limeTestModel()
	m.input.SetValue("add a flag")

	got := plainRender(t, m.composerBox(96))
	for _, want := range []string{"╭", "│", "❯ add a flag", "╰", "claude-sonnet-4.5", "auto-approve"} {
		if !strings.Contains(got, want) {
			t.Fatalf("composer box = %q, missing %q", got, want)
		}
	}
	if strings.Contains(got, "run ↵") {
		t.Fatalf("composer box = %q, should not show run hint", got)
	}
	assertRenderedLineWidths(t, got, 96)
}

func TestMalformedAskUserToolResultIsHiddenFromChatSurface(t *testing.T) {
	m := limeTestModel()
	row := transcriptRow{
		kind:   rowToolResult,
		id:     "call_bad",
		tool:   "ask_user",
		status: tools.StatusError,
		text:   "tool result: ask_user error",
		detail: "Error: Invalid arguments for ask_user: question 1 question is required",
	}
	if got := plainRender(t, m.renderRow(row, 96, buildRowContext([]transcriptRow{row}))); strings.TrimSpace(got) != "" {
		t.Fatalf("malformed ask_user result should stay internal, rendered %q", got)
	}
}

func TestMalformedToolArgumentResultIsHiddenFromChatSurface(t *testing.T) {
	m := limeTestModel()
	row := transcriptRow{
		kind:   rowToolResult,
		id:     "call_bad",
		tool:   "read_file",
		status: tools.StatusError,
		text:   "tool result: read_file error",
		detail: "Error: Failed to parse arguments for read_file: invalid character '{' after top-level value",
	}
	if got := plainRender(t, m.renderRow(row, 96, buildRowContext([]transcriptRow{row}))); strings.TrimSpace(got) != "" {
		t.Fatalf("malformed tool argument result should stay internal, rendered %q", got)
	}
}

func TestStatusLineGroups(t *testing.T) {
	m := limeTestModel()
	got := plainRender(t, m.statusLine(110))
	for _, want := range []string{"● anthropic"} {
		if !strings.Contains(got, want) {
			t.Fatalf("status line = %q, missing %q", got, want)
		}
	}
	if strings.Contains(got, "interactive") || strings.Contains(got, "claude-sonnet-4.5") || strings.Contains(got, "auto-approve") {
		t.Fatalf("status line = %q, should not include surface, model, or permission mode", got)
	}
	divider := plainRender(t, m.composerDividerLine(110))
	for _, want := range []string{"claude-sonnet-4.5", "auto-approve"} {
		if !strings.Contains(divider, want) {
			t.Fatalf("composer divider = %q, missing %q", divider, want)
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

// --- Regression tests for review-confirmed Stage 2 findings -----------------

func TestWrapPlainTextHandlesWideRunes(t *testing.T) {
	// CJK prose has no spaces: one giant double-width "word". This used to
	// panic (rune-count slicing) or emit lines ~2x the measure.
	lines := wrapPlainText(strings.Repeat("漢", 40), 20)
	if len(lines) == 0 {
		t.Fatal("expected wrapped output")
	}
	for _, line := range lines {
		if w := displayWidth(line); w > 20 {
			t.Fatalf("wrapped CJK line width %d exceeds measure 20: %q", w, line)
		}
	}
}

func displayWidth(line string) int {
	width := 0
	for _, glyph := range line {
		width += len([]rune{glyph})
		if glyph > 0x2E80 { // rough CJK double-width check for the test
			width++
		}
	}
	return width
}

func TestToolCardLinesAllSameWidth(t *testing.T) {
	m := limeTestModel()
	detail := "internal/cli/root.go:41: fs := flag.NewFlagSet"
	row := transcriptRow{kind: rowToolResult, id: "c", tool: "grep", status: tools.StatusOK, detail: detail}
	card := plainRender(t, m.renderRow(row, 60, buildRowContext(nil)))
	for _, line := range strings.Split(card, "\n") {
		if got := len([]rune(line)); got != 60 {
			t.Fatalf("card line width %d, want 60: %q", got, line)
		}
	}
}

func TestCancelRunClearsStreamingText(t *testing.T) {
	m := limeTestModel()
	m.pending = true
	m.activeRunID = 3
	m.streamingText = "partial answer from a doomed run"
	m.cancelRun()
	if m.streamingText != "" {
		t.Fatalf("cancelRun must clear streamingText, got %q", m.streamingText)
	}
}

func TestOrphanToolCardDoesNotAnimateOnLaterRuns(t *testing.T) {
	m := limeTestModel()
	// A call row from run 1 that never resolved; run 2 is now live.
	orphan := transcriptRow{kind: rowToolCall, id: "old", tool: "bash", runID: 1}
	m.pending = true
	m.activeRunID = 2
	got := plainRender(t, m.renderRow(orphan, 60, buildRowContext(nil)))
	if !strings.Contains(got, "…") {
		t.Fatalf("orphaned call card = %q, want static … placeholder, not a live spinner", got)
	}
}

func TestDiffPreambleLinesCarryNoGutterNumbers(t *testing.T) {
	m := limeTestModel()
	detail := strings.Join([]string{
		"stdout:",
		"diff --git a/foo.txt b/foo.txt",
		"index 1111111..2222222 100644",
		"--- a/foo.txt",
		"+++ b/foo.txt",
		"@@ -1,2 +1,2 @@",
		" alpha",
		"-old",
		`\ No newline at end of file`,
		"+new",
	}, "\n")
	row := transcriptRow{kind: rowToolResult, id: "c", tool: "bash", status: tools.StatusOK, detail: detail}
	got := plainRender(t, m.renderRow(row, 80, buildRowContext(nil)))
	if strings.Contains(got, "   0") {
		t.Fatalf("diff card = %q, preamble lines must not be numbered from 0", got)
	}
	// "+new" is the second line of the new file: the no-newline marker must
	// not have advanced the counter past 2.
	if !strings.Contains(got, "   2 + new") {
		t.Fatalf("diff card = %q, expected +new numbered 2", got)
	}
}

func TestSuggestionDigitsTypeNormallyWhilePending(t *testing.T) {
	m := limeTestModel()
	// /clear mid-run leaves the transcript empty while pending, so digits must
	// keep typing into the composer.
	m.pending = true
	m = typeRunes(t, m, "1")
	if got := m.input.Value(); got != "1" {
		t.Fatalf("digit while pending should type normally, got %q", got)
	}
}

func TestGrepCardHeadShowsTargetAndPatternColumns(t *testing.T) {
	m := limeTestModel()
	rows := []transcriptRow{
		{kind: rowToolCall, id: "c", tool: "grep", detail: "internal/cli", arg: `flag\.|RegisterFlag`},
		{kind: rowToolResult, id: "c", tool: "grep", status: tools.StatusOK, detail: "internal/cli/root.go:41: fs := flag.NewFlagSet"},
	}
	rc := buildRowContext(rows)
	got := plainRender(t, m.renderRow(rows[1], 110, rc))
	if !strings.Contains(got, "internal/cli") || !strings.Contains(got, `flag\.|RegisterFlag`) {
		t.Fatalf("grep card head = %q, want separate target and pattern columns", got)
	}
}

// --- Stage 3: interactive surfaces ------------------------------------------

func TestFocusedPermissionCardShowsBadgeRiskAndKeys(t *testing.T) {
	request := agent.PermissionRequest{
		ToolName:   "edit_file",
		Reason:     "writes internal/agent/exec.go",
		SideEffect: "write",
		Risk:       sandbox.Risk{Level: sandbox.RiskMedium},
	}
	got := plainRender(t, renderFocusedPermissionPrompt(request, 80))
	for _, want := range []string{"PERMISSION", "risk: medium", "edit_file", "writes internal/agent/exec.go", "[a] allow once", "[y] always", "[d] deny", "[esc]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("permission card = %q, missing %q", got, want)
		}
	}
}

func TestPermissionPromptCollapsesAfterDecision(t *testing.T) {
	m := limeTestModel()
	prompt := transcriptRow{kind: rowPermission, id: "call_1", permission: &agent.PermissionEvent{
		ToolCallID: "call_1", ToolName: "bash", Action: agent.PermissionActionPrompt,
	}}
	allowed := transcriptRow{kind: rowPermission, id: "call_1", permission: &agent.PermissionEvent{
		ToolCallID: "call_1", ToolName: "bash", Action: agent.PermissionActionAllow,
	}}
	rc := buildRowContext([]transcriptRow{prompt, allowed})

	if !rc.skip(prompt) {
		t.Fatal("a decided prompt row must collapse away")
	}
	if got := plainRender(t, m.renderRow(allowed, 80, rc)); !strings.Contains(got, "allowed once · bash") {
		t.Fatalf("manual allow = %q, want allowed once · bash", got)
	}

	grant := &agent.PermissionEvent{ToolCallID: "call_2", ToolName: "bash", Action: agent.PermissionActionAllow}
	grant.Grant = &sandbox.Grant{ToolName: "bash"}
	always := transcriptRow{kind: rowPermission, id: "call_2", permission: grant}
	promptTwo := transcriptRow{kind: rowPermission, id: "call_2", permission: &agent.PermissionEvent{
		ToolCallID: "call_2", ToolName: "bash", Action: agent.PermissionActionPrompt,
	}}
	rcTwo := buildRowContext([]transcriptRow{promptTwo, always})
	if got := plainRender(t, m.renderRow(always, 80, rcTwo)); !strings.Contains(got, "always · bash") {
		t.Fatalf("always allow = %q, want always · bash", got)
	}

	denied := transcriptRow{kind: rowPermission, id: "call_3", permission: &agent.PermissionEvent{
		ToolCallID: "call_3", ToolName: "bash", Action: agent.PermissionActionDeny,
	}}
	if got := plainRender(t, m.renderRow(denied, 80, buildRowContext(nil))); !strings.Contains(got, "denied · bash") {
		t.Fatalf("deny = %q, want denied · bash", got)
	}
}

func TestUnpromptedAllowRowsCollapseIntoAutoTag(t *testing.T) {
	allow := transcriptRow{kind: rowPermission, id: "call_1", permission: &agent.PermissionEvent{
		ToolCallID: "call_1", ToolName: "edit_file", Action: agent.PermissionActionAllow,
	}}
	rc := buildRowContext([]transcriptRow{allow})
	if !rc.skip(allow) {
		t.Fatal("an unprompted (auto) allow row must collapse — the tool card carries [auto]")
	}
}

func TestModelPickerRowsCarryCapabilityMeta(t *testing.T) {
	m := limeTestModel()
	picker := m.newModelPicker()
	if picker == nil {
		t.Fatal("expected a model picker")
	}
	withMeta := 0
	withCapability := 0
	for _, item := range picker.items {
		if strings.Contains(item.Meta, "K") || strings.Contains(item.Meta, "M") {
			withMeta++
		}
		if strings.Contains(item.Meta, "tools") || strings.Contains(item.Meta, "reasoning") || strings.Contains(item.Meta, "vision") {
			withCapability++
		}
		if strings.Contains(item.Meta, "API_KEY") {
			t.Fatalf("picker item %q leaked credential env metadata: %q", item.Value, item.Meta)
		}
		if !item.Remote && !item.Local {
			continue
		}
	}
	if withMeta == 0 {
		t.Fatalf("expected catalog models to expose ctx metadata, got %#v", picker.items[:minInt(3, len(picker.items))])
	}
	if withCapability == 0 {
		t.Fatalf("expected catalog models to expose capability metadata, got %#v", picker.items[:minInt(3, len(picker.items))])
	}

	m.picker = picker
	got := plainRender(t, m.pickerOverlay(100))
	for _, want := range []string{"Choose a model", "Enter select", "Ctrl+F favorite", "❯"} {
		if !strings.Contains(got, want) {
			t.Fatalf("picker overlay = %q, missing %q", got, want)
		}
	}
	if strings.Contains(got, "API_KEY") {
		t.Fatalf("picker overlay leaked credential env metadata: %q", got)
	}
}

func TestSpecReviewCardShowsBadgePathAndKeys(t *testing.T) {
	got := plainRender(t, renderFocusedSpecReviewPrompt(pendingSpecReviewPrompt{
		SpecFilePath: "/repo/specs/zero-1.md",
		RelativePath: "specs/zero-1.md",
	}, 80))
	for _, want := range []string{"SPEC REVIEW", "specs/zero-1.md", "[a] approve", "[r] reject", "[e] edit file", "[esc] cancel"} {
		if !strings.Contains(got, want) {
			t.Fatalf("spec review card = %q, missing %q", got, want)
		}
	}
}

func TestPermissionCollapseIsRunScoped(t *testing.T) {
	// Providers like Gemini synthesize repeating ToolCallIDs (gemini_tool_1 in
	// every turn). A decision from run 2 must not collapse run 1's undecided
	// prompt or steal its [auto]/hint attribution.
	runOnePrompt := transcriptRow{kind: rowPermission, id: "gemini_tool_1", runID: 1, permission: &agent.PermissionEvent{
		ToolCallID: "gemini_tool_1", ToolName: "bash", Action: agent.PermissionActionPrompt,
	}}
	runTwoDecision := transcriptRow{kind: rowPermission, id: "gemini_tool_1", runID: 2, permission: &agent.PermissionEvent{
		ToolCallID: "gemini_tool_1", ToolName: "bash", Action: agent.PermissionActionAllow,
	}}
	rc := buildRowContext([]transcriptRow{runOnePrompt, runTwoDecision})

	if rc.skip(runOnePrompt) {
		t.Fatal("run 1's undecided prompt must not collapse on run 2's decision")
	}
	// Run 2's allow had no prompt in run 2, so it reads as auto there.
	if !rc.skip(runTwoDecision) {
		t.Fatal("run 2's unprompted allow should fold into its tool card's [auto] tag")
	}

	// Same-run prompt+decision still collapses as before.
	sameRunPrompt := transcriptRow{kind: rowPermission, id: "gemini_tool_1", runID: 3, permission: &agent.PermissionEvent{
		ToolCallID: "gemini_tool_1", ToolName: "bash", Action: agent.PermissionActionPrompt,
	}}
	sameRunAllow := transcriptRow{kind: rowPermission, id: "gemini_tool_1", runID: 3, permission: &agent.PermissionEvent{
		ToolCallID: "gemini_tool_1", ToolName: "bash", Action: agent.PermissionActionAllow,
	}}
	rcSame := buildRowContext([]transcriptRow{sameRunPrompt, sameRunAllow})
	if !rcSame.skip(sameRunPrompt) {
		t.Fatal("a same-run decided prompt must collapse")
	}
	if rcSame.skip(sameRunAllow) {
		t.Fatal("the same-run manual decision line must render")
	}
}

func TestSessionsCardFieldsAreSanitized(t *testing.T) {
	if got := sanitizeCardField("evil\x1ftitle\nwith\x00bytes"); strings.ContainsAny(got, "\x1f\n\x00") {
		t.Fatalf("sanitizeCardField left separator bytes: %q", got)
	}
}
