package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/tools"
)

func attachedModel(t *testing.T) (model, *fakeExecSessionTool) {
	t.Helper()
	tool := &fakeExecSessionTool{
		sessions: []tools.ExecSessionSnapshot{{
			ID:           7,
			TTY:          true,
			Status:       "running",
			Command:      "sudo apt install x",
			RecentOutput: "[sudo] password for me: ",
			StartedAt:    time.Unix(100, 0),
		}},
	}
	m := modelWithFakeExecSessions(tool, time.Unix(200, 0))
	m.width = 100
	m.height = 30
	return m, tool
}

func TestOpenTerminalAttachSetsState(t *testing.T) {
	m, _ := attachedModel(t)
	next, cmd := m.openTerminalAttach(7)
	if cmd == nil {
		t.Fatal("openTerminalAttach should schedule a refresh tick")
	}
	state := next.terminalAttach
	if state == nil || state.sessionID != 7 {
		t.Fatalf("state = %#v", state)
	}
	if state.command != "sudo apt install x" || state.output != "[sudo] password for me: " {
		t.Fatalf("state did not pick up the snapshot: %#v", state)
	}
	if !next.terminalAttachSeen[7] {
		t.Fatal("openTerminalAttach should mark the session as seen")
	}
}

func TestTerminalAttachForwardsKeys(t *testing.T) {
	m, tool := attachedModel(t)
	next, _ := m.openTerminalAttach(7)

	for _, msg := range []tea.Msg{testKeyText("s"), testKey(tea.KeyEnter)} {
		updated, _ := next.Update(msg)
		next = updated.(model)
	}
	if next.terminalAttach == nil {
		t.Fatal("typing should stay attached")
	}
	if len(tool.writes) != 2 || string(tool.writes[0]) != "s" || string(tool.writes[1]) != "\r" {
		t.Fatalf("writes = %#v, want [s \\r]", tool.writes)
	}
}

func TestTerminalAttachForwardsCtrlC(t *testing.T) {
	m, tool := attachedModel(t)
	next, _ := m.openTerminalAttach(7)

	updated, _ := next.Update(testKeyCtrl('c'))
	next = updated.(model)
	if len(tool.writes) != 1 || len(tool.writes[0]) != 1 || tool.writes[0][0] != 0x03 {
		t.Fatalf("writes = %#v, want [0x03]", tool.writes)
	}
	if next.terminalAttach == nil {
		t.Fatal("Ctrl+C detached the session — it should go to the process")
	}
	if next.exitConfirmActive {
		t.Fatal("Ctrl+C armed the exit confirmation while attached")
	}
}

func TestTerminalAttachForwardsPaste(t *testing.T) {
	m, tool := attachedModel(t)
	next, _ := m.openTerminalAttach(7)

	updated, _ := next.Update(testPaste("pw"))
	next = updated.(model)
	if next.terminalAttach == nil {
		t.Fatal("paste should stay attached")
	}
	if len(tool.writes) != 1 || string(tool.writes[0]) != "pw" {
		t.Fatalf("writes = %#v, want [pw]", tool.writes)
	}
}

func TestTerminalAttachEscDetaches(t *testing.T) {
	m, tool := attachedModel(t)
	next, _ := m.openTerminalAttach(7)

	updated, _ := next.Update(testKey(tea.KeyEsc))
	next = updated.(model)
	if next.terminalAttach != nil {
		t.Fatal("Esc should detach")
	}
	if len(tool.writes) != 0 {
		t.Fatalf("Esc forwarded bytes to the process: %#v", tool.writes)
	}
	if !strings.Contains(next.transientNotice.text, "Detached from session 7") {
		t.Fatalf("notice = %q", next.transientNotice.text)
	}
}

func TestTerminalAttachTickAutoClosesOnExit(t *testing.T) {
	tool := &fakeExecSessionTool{
		sessions: []tools.ExecSessionSnapshot{{
			ID: 7, TTY: true, Status: "running", Command: "sudo apt install x",
			StartedAt: time.Unix(100, 0),
		}},
	}
	m := modelWithFakeExecSessions(tool, time.Unix(200, 0))
	next, _ := m.openTerminalAttach(7)

	exitCode := 1
	tool.sessions[0].Status = "exited"
	tool.sessions[0].ExitCode = &exitCode
	updated, _ := next.Update(terminalAttachTickMsg{})
	next = updated.(model)
	if next.terminalAttach != nil {
		t.Fatal("overlay should close itself when the session exits")
	}
	if !strings.Contains(next.transientNotice.text, "Terminal session 7 finished (exit 1).") {
		t.Fatalf("notice = %q", next.transientNotice.text)
	}
}

func TestTerminalAttachTickAutoClosesOnMissing(t *testing.T) {
	tool := &fakeExecSessionTool{
		sessions: []tools.ExecSessionSnapshot{{
			ID: 7, TTY: true, Status: "running", Command: "cat", StartedAt: time.Unix(100, 0),
		}},
	}
	m := modelWithFakeExecSessions(tool, time.Unix(200, 0))
	next, _ := m.openTerminalAttach(7)

	tool.sessions = nil
	updated, _ := next.Update(terminalAttachTickMsg{})
	next = updated.(model)
	if next.terminalAttach != nil {
		t.Fatal("overlay should close itself when the session disappears")
	}
	if !strings.Contains(next.transientNotice.text, "Terminal session 7 ended.") {
		t.Fatalf("notice = %q", next.transientNotice.text)
	}
}

func TestTerminalAttachOverlay(t *testing.T) {
	m, _ := attachedModel(t)
	next, _ := m.openTerminalAttach(7)

	overlay := next.terminalAttachOverlay(80)
	for _, want := range []string{"session 7", "[sudo] password for me:", "Esc detach", "/stop 7"} {
		if !strings.Contains(overlay, want) {
			t.Fatalf("overlay missing %q:\n%s", want, overlay)
		}
	}
}

func TestExecCallWantsTTY(t *testing.T) {
	cases := []struct {
		name string
		args string
		want bool
	}{
		{"tty true", `{"cmd":"x","tty":true}`, true},
		{"tty false", `{"cmd":"x","tty":false}`, false},
		{"tty absent", `{"cmd":"x"}`, false},
		{"garbage", `not json`, false},
		{"empty", ``, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := execCallWantsTTY(tc.args); got != tc.want {
				t.Fatalf("execCallWantsTTY(%q) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

func TestInteractiveExecStartAutoAttaches(t *testing.T) {
	tool := &fakeExecSessionTool{}
	m := modelWithFakeExecSessions(tool, time.Unix(200, 0))
	m.activeRunID = 3
	m.pending = true

	updated, cmd := m.Update(interactiveExecStartMsg{runID: 3})
	next := updated.(model)
	if next.terminalAutoAttach == nil || cmd == nil {
		t.Fatal("interactiveExecStartMsg should arm the watcher and a tick")
	}

	tool.sessions = append(tool.sessions, tools.ExecSessionSnapshot{
		ID: 7, TTY: true, Status: "running", Command: "cat", StartedAt: time.Unix(100, 0),
	})
	updated, _ = next.Update(terminalAutoAttachTickMsg{})
	next = updated.(model)
	if next.terminalAttach == nil || next.terminalAttach.sessionID != 7 {
		t.Fatalf("auto-attach did not open on the new session: %#v", next.terminalAttach)
	}
	if !next.terminalAttachSeen[7] {
		t.Fatal("session 7 should be marked seen")
	}
	if next.terminalAutoAttach != nil {
		t.Fatal("watcher should stop once it attached")
	}
}

func TestTerminalAutoAttachSkipsKnownSession(t *testing.T) {
	tool := &fakeExecSessionTool{
		sessions: []tools.ExecSessionSnapshot{
			{ID: 5, TTY: true, Status: "running", Command: "top", StartedAt: time.Unix(100, 0)},
		},
	}
	m := modelWithFakeExecSessions(tool, time.Unix(200, 0))
	m.activeRunID = 3
	m.pending = true

	// Session 5 predates the tool call — it is not this call's session.
	updated, _ := m.Update(interactiveExecStartMsg{runID: 3})
	next := updated.(model)
	updated, cmd := next.Update(terminalAutoAttachTickMsg{})
	next = updated.(model)
	if next.terminalAttach != nil {
		t.Fatal("watcher must not attach to a session that was already running")
	}
	if cmd == nil || next.terminalAutoAttach == nil {
		t.Fatal("watcher should keep ticking while nothing new appears")
	}

	tool.sessions = append(tool.sessions, tools.ExecSessionSnapshot{
		ID: 6, TTY: true, Status: "running", Command: "cat", StartedAt: time.Unix(200, 0),
	})
	updated, _ = next.Update(terminalAutoAttachTickMsg{})
	next = updated.(model)
	if next.terminalAttach == nil || next.terminalAttach.sessionID != 6 {
		t.Fatalf("watcher should attach to the new session 6: %#v", next.terminalAttach)
	}
}

func TestTerminalAutoAttachDoesNotReattachSeen(t *testing.T) {
	tool := &fakeExecSessionTool{
		sessions: []tools.ExecSessionSnapshot{
			{ID: 7, TTY: true, Status: "running", Command: "cat", StartedAt: time.Unix(100, 0)},
		},
	}
	m := modelWithFakeExecSessions(tool, time.Unix(200, 0))
	m.activeRunID = 3
	m.pending = true
	m.terminalAttachSeen = map[int]bool{7: true} // user already attached and detached

	updated, _ := m.Update(interactiveExecStartMsg{runID: 3})
	next := updated.(model)
	updated, cmd := next.Update(terminalAutoAttachTickMsg{})
	next = updated.(model)
	if next.terminalAttach != nil {
		t.Fatal("watcher must not re-open a session the user detached")
	}
	if cmd == nil {
		t.Fatal("watcher should keep ticking")
	}
}

func TestTerminalAutoAttachIgnoresWrongRun(t *testing.T) {
	m, _ := attachedModel(t)
	m.activeRunID = 3
	m.pending = true

	updated, cmd := m.Update(interactiveExecStartMsg{runID: 9})
	next := updated.(model)
	if next.terminalAutoAttach != nil || cmd != nil {
		t.Fatal("a msg for another run must not arm the watcher")
	}
}

func TestTerminalAutoAttachWaitsForPermissionPrompt(t *testing.T) {
	tool := &fakeExecSessionTool{
		sessions: []tools.ExecSessionSnapshot{
			{ID: 7, TTY: true, Status: "running", Command: "cat", StartedAt: time.Unix(100, 0)},
		},
	}
	m := modelWithFakeExecSessions(tool, time.Unix(200, 0))
	m.activeRunID = 3
	m.pending = true
	m.pendingPermission = &pendingPermissionPrompt{
		request: agent.PermissionRequest{ToolName: "exec_command"},
		decide:  func(agent.PermissionDecision) {},
	}

	// The watcher is armed before the session registers? Session already here —
	// simplest equivalent: arm it directly and confirm the modal blocks opening.
	m.terminalAutoAttach = &terminalAutoAttachState{
		runID: 3, known: map[int]bool{}, deadline: m.now().Add(terminalAutoAttachTimeout),
	}
	updated, cmd := m.Update(terminalAutoAttachTickMsg{})
	next := updated.(model)
	if next.terminalAttach != nil {
		t.Fatal("overlay must not open over a permission prompt")
	}
	if cmd == nil {
		t.Fatal("watcher should keep ticking while the modal is up")
	}

	next.pendingPermission = nil
	updated, _ = next.Update(terminalAutoAttachTickMsg{})
	next = updated.(model)
	if next.terminalAttach == nil || next.terminalAttach.sessionID != 7 {
		t.Fatalf("overlay should open once the modal clears: %#v", next.terminalAttach)
	}
}

// The user's keystrokes go to the PTY and nowhere else: not the transcript,
// not the composer, not a notice.
func TestTerminalAttachNeverEchoesInput(t *testing.T) {
	m, tool := attachedModel(t)
	next, _ := m.openTerminalAttach(7)

	for _, msg := range []tea.Msg{testKeyText("s"), testKeyText("3"), testKeyText("c"), testKeyText("r"), testKeyText("e"), testKeyText("t"), testPaste("pw")} {
		updated, _ := next.Update(msg)
		next = updated.(model)
	}
	if len(tool.writes) == 0 {
		t.Fatal("nothing was forwarded")
	}
	for _, row := range next.transcript {
		if strings.Contains(row.text, "s3cret") || strings.Contains(row.text, "pw") {
			t.Fatalf("typed bytes leaked into transcript row: %q", row.text)
		}
	}
	if strings.Contains(next.transientNotice.text, "s3cret") || strings.Contains(next.transientNotice.text, "pw") {
		t.Fatalf("typed bytes leaked into the notice: %q", next.transientNotice.text)
	}
	if strings.Contains(next.composerValue(), "s3cret") {
		t.Fatal("typed bytes leaked into the composer")
	}
}

func TestTerminalAttachSwallowsMouse(t *testing.T) {
	m, _ := attachedModel(t)
	next, _ := m.openTerminalAttach(7)
	updated, _ := next.Update(testMouseClick(tea.MouseLeft, 5, 5))
	next = updated.(model)
	if next.terminalAttach == nil {
		t.Fatal("mouse click detached the session")
	}
}

func TestPtyInputBytes(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.KeyMsg
		want string
		ok   bool
	}{
		{"printable", testKeyText("a"), "a", true},
		{"space", testKey(tea.KeySpace), " ", true},
		{"enter", testKey(tea.KeyEnter), "\r", true},
		{"tab", testKey(tea.KeyTab), "\t", true},
		{"backspace", testKey(tea.KeyBackspace), "\x7f", true},
		{"delete", testKey(tea.KeyDelete), "\x1b[3~", true},
		{"up", testKey(tea.KeyUp), "\x1b[A", true},
		{"down", testKey(tea.KeyDown), "\x1b[B", true},
		{"right", testKey(tea.KeyRight), "\x1b[C", true},
		{"left", testKey(tea.KeyLeft), "\x1b[D", true},
		{"home", testKey(tea.KeyHome), "\x1b[H", true},
		{"end", testKey(tea.KeyEnd), "\x1b[F", true},
		{"pgup", testKey(tea.KeyPgUp), "\x1b[5~", true},
		{"pgdn", testKey(tea.KeyPgDown), "\x1b[6~", true},
		{"ctrl+c", testKeyCtrl('c'), "\x03", true},
		{"ctrl+d", testKeyCtrl('d'), "\x04", true},
		{"ctrl+a", testKeyCtrl('a'), "\x01", true},
		{"alt+x", testKeyAltText("x"), "\x1bx", true},
		{"esc unmapped", testKey(tea.KeyEsc), "", false},
		{"ctrl+1 unmapped", testKeyCtrl('1'), "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ptyInputBytes(tc.msg)
			if ok != tc.ok || string(got) != tc.want {
				t.Fatalf("ptyInputBytes = %q, %v; want %q, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestRenderTerminalTail(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		width int
		rows  int
		want  []string
	}{
		{
			name:  "tail and padding",
			raw:   "one\ntwo\nthree",
			width: 10, rows: 4,
			want: []string{"", "one", "two", "three"},
		},
		{
			name:  "keeps last rows",
			raw:   "a\nb\nc\nd",
			width: 10, rows: 2,
			want: []string{"c", "d"},
		},
		{
			name:  "carriage return overwrites",
			raw:   "progress 10%\rprogress 90%",
			width: 20, rows: 1,
			want: []string{"progress 90%"},
		},
		{
			name:  "shorter overwrite keeps remainder",
			raw:   "downloading...\r100%",
			width: 20, rows: 1,
			want: []string{"100%loading..."},
		},
		{
			name:  "crlf normalized",
			raw:   "line1\r\nline2",
			width: 10, rows: 2,
			want: []string{"line1", "line2"},
		},
		{
			name:  "ansi stripped",
			raw:   "\x1b[31mred\x1b[0m plain",
			width: 20, rows: 1,
			want: []string{"red plain"},
		},
		{
			name:  "control runes dropped, tab expanded",
			raw:   "a\x07b\tc",
			width: 20, rows: 1,
			want: []string{"ab    c"},
		},
		{
			name:  "width truncation",
			raw:   "abcdefghijklmnop",
			width: 5, rows: 1,
			want: []string{"abcd…"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderTerminalTail(tc.raw, tc.width, tc.rows)
			if len(got) != len(tc.want) {
				t.Fatalf("renderTerminalTail = %#v, want %#v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("renderTerminalTail[%d] = %q, want %q (all: %#v)", i, got[i], tc.want[i], got)
				}
			}
		})
	}
}
