package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/sandbox"
	"github.com/Gitlawb/zero/internal/tools"
)

// The spec's adaptive acceptance criteria: a table across widths asserting
// which segments render at each tier.
func TestWidthTierSegments(t *testing.T) {
	diff := strings.Join([]string{
		"+++ b/internal/cli/root.go",
		"@@ -1,2 +1,2 @@",
		" package cli",
		"-var old = 1",
		"+var new = 2",
	}, "\n")

	cases := []struct {
		width        int
		wantCtx      bool // header context window ("200K")
		wantCwd      bool // header cwd segment
		wantArg      bool // tool-card arg column
		wantGutter   bool // diff line-number gutter
		wantInteract bool // status "interactive" group
		wantModelID  bool // status model id group
	}{
		{width: 58, wantCtx: false, wantCwd: false, wantArg: false, wantGutter: false, wantInteract: false, wantModelID: false},
		{width: 70, wantCtx: false, wantCwd: false, wantArg: false, wantGutter: false, wantInteract: false, wantModelID: false},
		{width: 80, wantCtx: false, wantCwd: true, wantArg: false, wantGutter: true, wantInteract: false, wantModelID: true},
		{width: 100, wantCtx: true, wantCwd: true, wantArg: true, wantGutter: true, wantInteract: true, wantModelID: true},
		{width: 120, wantCtx: true, wantCwd: true, wantArg: true, wantGutter: true, wantInteract: true, wantModelID: true},
	}

	for _, tc := range cases {
		m := newModel(context.Background(), Options{
			Cwd:          "/Users/dev/zero-project-workspace",
			ProviderName: "anthropic",
			ModelName:    "claude-sonnet-4.5",
		})
		m.width, m.height = tc.width, 30

		title := plainRender(t, m.titleBar(tc.width))
		if got := strings.Contains(title, "200K"); got != tc.wantCtx {
			t.Errorf("width %d: title ctx = %v, want %v (%q)", tc.width, got, tc.wantCtx, title)
		}
		if got := strings.Contains(title, "zero-project-workspace"); got != tc.wantCwd {
			t.Errorf("width %d: title cwd = %v, want %v (%q)", tc.width, got, tc.wantCwd, title)
		}

		rows := []transcriptRow{
			{kind: rowToolCall, id: "c", tool: "grep", detail: "internal/cli", arg: "RegisterFlag"},
			{kind: rowToolResult, id: "c", tool: "grep", status: tools.StatusOK, detail: "internal/cli/root.go:41: x"},
		}
		rc := buildRowContext(rows)
		card := plainRender(t, m.renderRow(rows[1], tc.width, rc))
		if got := strings.Contains(card, "RegisterFlag"); got != tc.wantArg {
			t.Errorf("width %d: card arg column = %v, want %v (%q)", tc.width, got, tc.wantArg, card)
		}

		diffRow := transcriptRow{kind: rowToolResult, id: "d", tool: "edit_file", status: tools.StatusOK, detail: diff}
		diffCard := plainRender(t, m.renderRow(diffRow, tc.width, buildRowContext(nil)))
		if got := strings.Contains(diffCard, "   2 + var new = 2") || strings.Contains(diffCard, "   2 +"); got != tc.wantGutter {
			t.Errorf("width %d: diff gutter = %v, want %v (%q)", tc.width, got, tc.wantGutter, diffCard)
		}

		status := plainRender(t, m.statusLine(tc.width))
		if got := strings.Contains(status, "interactive"); got != tc.wantInteract {
			t.Errorf("width %d: status interactive = %v, want %v (%q)", tc.width, got, tc.wantInteract, status)
		}
		if got := strings.Contains(status, "claude-sonnet-4.5"); got != tc.wantModelID {
			t.Errorf("width %d: status model id = %v, want %v (%q)", tc.width, got, tc.wantModelID, status)
		}
		if !strings.Contains(status, "⏵⏵") {
			t.Errorf("width %d: status must always keep the mode group (%q)", tc.width, status)
		}
	}
}

func TestTinyTierSingleSegmentAndRailLessCards(t *testing.T) {
	m := newModel(context.Background(), Options{ProviderName: "anthropic", ModelName: "claude-sonnet-4.5"})
	m.width, m.height = 40, 20

	status := plainRender(t, m.statusLine(40))
	if strings.Contains(status, "anthropic") {
		t.Fatalf("tiny status = %q, want mode line only", status)
	}
	if !strings.Contains(status, "⏵⏵") {
		t.Fatalf("tiny status = %q, want the mode group", status)
	}

	row := transcriptRow{kind: rowToolResult, id: "c", tool: "grep", status: tools.StatusOK, detail: "a.go:1: x"}
	card := plainRender(t, m.renderRow(row, 40, buildRowContext(nil)))
	for _, line := range strings.Split(card, "\n") {
		if strings.HasPrefix(line, "│") || strings.HasSuffix(line, "│") {
			t.Fatalf("tiny card keeps side borders: %q", line)
		}
	}
}

// The spec's hard rendering invariant: never emit a styled line wider than
// the terminal, across the whole frame at every tier — including the empty
// state, ask-user rows, permission details, and pending image chips, which
// each overflowed at some width before being fitted.
func TestViewNeverExceedsTerminalWidth(t *testing.T) {
	diff := "+++ b/a.go\n@@ -1,1 +1,1 @@\n-old line that is reasonably long for the card\n+new line that is reasonably long for the card"
	for _, width := range []int{24, 40, 58, 70, 80, 100, 120} {
		m := newModel(context.Background(), Options{
			Cwd:          "/Users/dev/zero-project-workspace",
			ProviderName: "anthropic",
			ModelName:    "claude-sonnet-4.5",
		})
		m.width, m.height = width, 24

		// Empty state first: the centered tagline/hint must also fit.
		for index, line := range strings.Split(m.View(), "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Fatalf("width %d: empty-state line %d is %d cells wide: %q", width, index, got, line)
			}
		}

		m.pendingImageLabels = []string{"Screenshot 2026-06-10 at 09.41.13.png", "Screenshot 2026-06-10 at 09.44.02.png"}
		m.transcript = append(m.transcript,
			transcriptRow{kind: rowUser, text: "please change the longest line in the file to something even longer than before"},
			transcriptRow{kind: rowToolCall, id: "c1", tool: "grep", detail: "internal/cli", arg: "RegisterFlag|flag\\."},
			transcriptRow{kind: rowToolResult, id: "c1", tool: "grep", status: tools.StatusOK, detail: "internal/cli/root.go:41: fs := flag.NewFlagSet(\"zero\", flag.ContinueOnError)"},
			transcriptRow{kind: rowToolResult, id: "c2", tool: "edit_file", status: tools.StatusOK, detail: diff},
			transcriptRow{kind: rowSystem, text: "Mode set to ask."},
			transcriptRow{kind: rowAskUser, id: "ask1", text: "ask_user: which of these very long alternative naming schemes should the new flag adopt", detail: "1. choose between --version and --print-version  (--version, --print-version, keep both and alias them)"},
			transcriptRow{kind: rowPermission, id: "p1", permission: &permissionEventLongDetailFixture},
			transcriptRow{kind: rowAssistant, text: "Done — the change is in.", final: true, turnTools: 2},
		)

		for index, line := range strings.Split(m.View(), "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Fatalf("width %d: frame line %d is %d cells wide: %q", width, index, got, line)
			}
		}
	}
}

var permissionEventLongDetailFixture = agent.PermissionEvent{
	ToolCallID:     "p1",
	ToolName:       "bash",
	Action:         agent.PermissionActionPrompt,
	Permission:     "prompt",
	PermissionMode: agent.PermissionModeAsk,
	SideEffect:     "runs `go test ./... -timeout 600s` in /Users/dev/zero-project-workspace with network access",
	Reason:         "command writes outside the workspace and downloads modules from the network proxy",
	Risk:           sandbox.Risk{Level: sandbox.RiskMedium},
}
