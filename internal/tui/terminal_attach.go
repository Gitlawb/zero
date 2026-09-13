package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gitlawb/zero/internal/execution"
)

// terminalAttachState is the modal "attach to a live terminal" overlay: while
// it is set, keystrokes are forwarded verbatim to the session's PTY stdin and
// the overlay shows a rolling tail of its output. The bytes the user types are
// never inspected, stored, or logged — the PTY's own line discipline (e.g.
// sudo's no-echo prompt) handles masking.
type terminalAttachState struct {
	sessionID int
	command   string
	output    string
}

const terminalAttachTickInterval = 100 * time.Millisecond

type terminalAttachTickMsg struct{}

func terminalAttachTickCmd() tea.Cmd {
	return tea.Tick(terminalAttachTickInterval, func(time.Time) tea.Msg {
		return terminalAttachTickMsg{}
	})
}

// terminalAutoAttachState watches a run for a brand-new tty exec session so the
// attach overlay can open on it without the user typing /attach.
type terminalAutoAttachState struct {
	runID    int
	known    map[int]bool
	deadline time.Time
}

const terminalAutoAttachTimeout = 60 * time.Second

// interactiveExecStartMsg signals that the active run just invoked exec_command
// with tty:true; the session registers with the process manager moments later,
// so the tick polls for it.
type interactiveExecStartMsg struct {
	runID int
}

type terminalAutoAttachTickMsg struct{}

func terminalAutoAttachTickCmd() tea.Cmd {
	return tea.Tick(terminalAttachTickInterval, func(time.Time) tea.Msg {
		return terminalAutoAttachTickMsg{}
	})
}

// execCallWantsTTY reports whether an exec_command tool call's arguments JSON
// requests a PTY. Malformed JSON means false.
func execCallWantsTTY(arguments string) bool {
	var args struct {
		TTY bool `json:"tty"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return false
	}
	return args.TTY
}

// openTerminalAttach attaches to an interactive exec session. Callers check
// TTY eligibility first; a session that is already gone or exited surfaces as a
// transient notice instead of an empty overlay.
func (m model) openTerminalAttach(id int) (model, tea.Cmd) {
	state := &terminalAttachState{sessionID: id}
	if controller, ok := m.execSessionController(); ok {
		snapshot, found := controller.ExecSession(id)
		switch {
		case !found:
			return m.showTransientNoticeInline(fmt.Sprintf("Terminal session %d ended.", id), transientNoticeInfo), nil
		case snapshot.Status != "running":
			exitCode := 0
			if snapshot.ExitCode != nil {
				exitCode = *snapshot.ExitCode
			}
			return m.showTransientNoticeInline(fmt.Sprintf("Terminal session %d finished (exit %d).", id, exitCode), transientNoticeInfo), nil
		default:
			state.command = snapshot.Command
			state.output = snapshot.RecentOutput
		}
	}
	m.terminalAttach = state
	if m.terminalAttachSeen == nil {
		m.terminalAttachSeen = map[int]bool{}
	}
	m.terminalAttachSeen[id] = true
	m.clearSuggestions()
	return m, terminalAttachTickCmd()
}

// refreshTerminalAttach polls the session snapshot for the overlay tail. When
// the session exits or disappears the overlay closes itself with a notice;
// detaching also stops the tick because the handler drops it while
// terminalAttach is nil.
func (m model) refreshTerminalAttach() (model, tea.Cmd) {
	state := m.terminalAttach
	if state == nil {
		return m, nil
	}
	controller, ok := m.execSessionController()
	if !ok {
		m.terminalAttach = nil
		return m, nil
	}
	snapshot, found := controller.ExecSession(state.sessionID)
	if !found {
		m.terminalAttach = nil
		return m.showTransientNoticeInline(fmt.Sprintf("Terminal session %d ended.", state.sessionID), transientNoticeInfo), nil
	}
	state.output = snapshot.RecentOutput
	if snapshot.Status != "running" {
		m.terminalAttach = nil
		exitCode := 0
		if snapshot.ExitCode != nil {
			exitCode = *snapshot.ExitCode
		}
		return m.showTransientNoticeInline(fmt.Sprintf("Terminal session %d finished (exit %d).", state.sessionID, exitCode), transientNoticeInfo), nil
	}
	return m, terminalAttachTickCmd()
}

// pollTerminalAutoAttach runs the auto-attach watcher: while the run is live it
// looks for a new tty session and opens the overlay on it, giving up at the
// deadline (a permission prompt can hold the session start for a while).
func (m model) pollTerminalAutoAttach() (model, tea.Cmd) {
	state := m.terminalAutoAttach
	if state == nil {
		return m, nil
	}
	if m.activeRunID != state.runID || !m.pending || m.now().After(state.deadline) {
		m.terminalAutoAttach = nil
		return m, nil
	}
	controller, ok := m.execSessionController()
	if !ok {
		return m, terminalAutoAttachTickCmd()
	}
	for _, session := range controller.ExecSessions() {
		if !session.TTY || session.Status != "running" || state.known[session.ID] || m.terminalAttachSeen[session.ID] {
			continue
		}
		if !m.noBlockingModalExceptAttach() || m.terminalAttach != nil {
			// A modal (permission prompt, picker, …) or an existing attach owns
			// the viewport; keep watching so the overlay opens once it clears.
			return m, terminalAutoAttachTickCmd()
		}
		m.terminalAutoAttach = nil
		return m.openTerminalAttach(session.ID)
	}
	return m, terminalAutoAttachTickCmd()
}

// noBlockingModalExceptAttach is noBlockingModal without the attach overlay's
// own term, for the auto-attach watcher deciding whether another modal owns
// the viewport.
func (m model) noBlockingModalExceptAttach() bool {
	return m.pendingPermission == nil && m.pendingAskUser == nil && m.pendingSpecReview == nil &&
		m.providerWizard == nil && m.mcpAddWizard == nil && m.mcpManager == nil && m.picker == nil &&
		m.sttKeyPrompt == nil && m.renamePrompt == nil
}

// writeTerminalAttachInput forwards bytes to the session stdin. The bytes are
// written through untouched and never echoed into the transcript, a notice, or
// the overlay. Errors only update overlay state or a generic notice.
func (m model) writeTerminalAttachInput(state *terminalAttachState, data []byte) model {
	controller, ok := m.execSessionController()
	if !ok {
		m.terminalAttach = nil
		return m
	}
	err := controller.WriteExecSessionInput(state.sessionID, data)
	switch {
	case errors.Is(err, execution.ErrProcessNotFound):
		m.terminalAttach = nil
		return m.showTransientNoticeInline(fmt.Sprintf("Terminal session %d ended.", state.sessionID), transientNoticeInfo)
	case errors.Is(err, execution.ErrProcessStdinDisabled):
		m.terminalAttach = nil
		return m.showTransientNoticeInline(fmt.Sprintf("Terminal session %d no longer accepts input.", state.sessionID), transientNoticeWarning)
	case err != nil:
		return m.showTransientNoticeInline("Could not send input to session "+strconv.Itoa(state.sessionID)+".", transientNoticeWarning)
	}
	return m
}

// handleTerminalAttachKey owns every keystroke while attached: Esc detaches,
// anything else maps to PTY bytes and goes to the process (so Ctrl+C reaches
// it as 0x03 instead of triggering the TUI's own exit confirmation).
func (m model) handleTerminalAttachKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	state := m.terminalAttach
	if keyIs(msg, tea.KeyEsc) {
		m.terminalAttach = nil
		return m.showTransientNoticeInline(
			fmt.Sprintf("Detached from session %d — it keeps running; /attach %d to return.", state.sessionID, state.sessionID),
			transientNoticeInfo), nil
	}
	data, ok := ptyInputBytes(msg)
	if !ok {
		return m, nil
	}
	return m.writeTerminalAttachInput(state, data), nil
}

// handleTerminalAttachPaste forwards a bracketed paste verbatim to the PTY.
func (m model) handleTerminalAttachPaste(content string) (tea.Model, tea.Cmd) {
	state := m.terminalAttach
	if content == "" {
		return m, nil
	}
	return m.writeTerminalAttachInput(state, []byte(content)), nil
}

// ptyInputBytes maps a keystroke to the byte sequence a terminal would send.
// Esc is deliberately unmapped: it detaches instead of reaching the process.
func ptyInputBytes(msg tea.KeyMsg) ([]byte, bool) {
	key := msg.Key()
	if keyHasMod(msg, tea.ModCtrl) {
		if key.Code >= 'a' && key.Code <= 'z' {
			return []byte{byte(key.Code - 'a' + 1)}, true
		}
		return nil, false
	}
	if keyAlt(msg) {
		if text := keyText(msg); text != "" {
			return append([]byte{0x1b}, text...), true
		}
		return nil, false
	}
	switch key.Code {
	case tea.KeyEnter:
		return []byte("\r"), true
	case tea.KeyTab:
		return []byte("\t"), true
	case tea.KeyBackspace:
		return []byte{0x7f}, true
	case tea.KeyDelete:
		return []byte("\x1b[3~"), true
	case tea.KeyUp:
		return []byte("\x1b[A"), true
	case tea.KeyDown:
		return []byte("\x1b[B"), true
	case tea.KeyRight:
		return []byte("\x1b[C"), true
	case tea.KeyLeft:
		return []byte("\x1b[D"), true
	case tea.KeyHome:
		return []byte("\x1b[H"), true
	case tea.KeyEnd:
		return []byte("\x1b[F"), true
	case tea.KeyPgUp:
		return []byte("\x1b[5~"), true
	case tea.KeyPgDown:
		return []byte("\x1b[6~"), true
	}
	if text := keyText(msg); text != "" {
		return []byte(text), true
	}
	return nil, false
}

// renderTerminalTail turns raw PTY output into display lines: carriage returns
// overwrite their line from column 0 (progress-bar style), ANSI sequences are
// stripped, remaining control runes are dropped (tab expands to spaces), and
// the result is the last rows lines padded to exactly rows.
func renderTerminalTail(raw string, width, rows int) []string {
	if rows < 0 {
		rows = 0
	}
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	source := strings.Split(raw, "\n")
	lines := make([]string, 0, len(source))
	for _, line := range source {
		lines = append(lines, sanitizeTerminalLine(line, width))
	}
	if len(lines) > rows {
		lines = lines[len(lines)-rows:]
	}
	for len(lines) < rows {
		lines = append([]string{""}, lines...)
	}
	return lines
}

func sanitizeTerminalLine(line string, width int) string {
	var resolved []rune
	for _, segment := range strings.Split(line, "\r") {
		runes := []rune(segment)
		if len(runes) < len(resolved) {
			runes = append(runes, resolved[len(runes):]...)
		}
		resolved = runes
	}
	stripped := ansi.Strip(string(resolved))
	var builder strings.Builder
	for _, r := range stripped {
		switch {
		case r == '\t':
			builder.WriteString("    ")
		case unicode.IsControl(r):
		default:
			builder.WriteRune(r)
		}
	}
	if width <= 0 {
		return builder.String()
	}
	return fitStyledLine(builder.String(), width)
}

func terminalAttachRows(height int) int {
	if height <= 0 {
		return 10
	}
	return minInt(16, maxInt(6, height-10))
}

func (m model) terminalAttachOverlay(width int) string {
	state := m.terminalAttach
	if state == nil {
		return ""
	}
	overlayWidth := minInt(width, pickerOverlayMaxWidth)
	if overlayWidth < pickerOverlayMinWidth {
		overlayWidth = width
	}
	innerWidth := overlayWidth - 4
	command := compactCommandOutputText(state.command)
	if command == "" {
		command = "command"
	}
	// Reserve one cell so the cursor never pushes a full line past the box edge.
	tail := renderTerminalTail(state.output, innerWidth-1, terminalAttachRows(m.height))
	if len(tail) > 0 {
		tail[len(tail)-1] = tail[len(tail)-1] + zeroTheme.accent.Render("▌")
	}
	footer := fmt.Sprintf("type to send · ⏎ Enter · Esc detach · Ctrl+C goes to the process · /stop %d to kill", state.sessionID)
	lines := []string{zeroTheme.faint.Render(command), ""}
	lines = append(lines, tail...)
	lines = append(lines, "", zeroTheme.faint.Render(footer))
	title := fmt.Sprintf("Terminal · session %d", state.sessionID)
	return centerRenderedBlock(styledBlockFillTitle(overlayWidth, title, lines, zeroTheme.lineStrong, lipgloss.NewStyle()), width)
}
