package tui

import (
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
	exited    bool
	exitCode  int
	missing   bool
	note      string
}

const terminalAttachTickInterval = 100 * time.Millisecond

type terminalAttachTickMsg struct{}

func terminalAttachTickCmd() tea.Cmd {
	return tea.Tick(terminalAttachTickInterval, func(time.Time) tea.Msg {
		return terminalAttachTickMsg{}
	})
}

// openTerminalAttach attaches to an interactive exec session. Callers check
// TTY eligibility first; a missing snapshot still opens the overlay in its
// ended state so the user sees what happened.
func (m model) openTerminalAttach(id int) (model, tea.Cmd) {
	state := &terminalAttachState{sessionID: id}
	if controller, ok := m.execSessionController(); ok {
		if snapshot, found := controller.ExecSession(id); found {
			state.command = snapshot.Command
			state.output = snapshot.RecentOutput
			if snapshot.Status != "running" {
				state.exited = true
				if snapshot.ExitCode != nil {
					state.exitCode = *snapshot.ExitCode
				}
			}
		} else {
			state.missing = true
			state.exited = true
		}
	}
	m.terminalAttach = state
	m.clearSuggestions()
	return m, terminalAttachTickCmd()
}

// refreshTerminalAttach polls the session snapshot for the overlay tail. The
// tick stops itself once the session ends; detaching also stops it because the
// handler drops ticks while terminalAttach is nil.
func (m model) refreshTerminalAttach() (model, tea.Cmd) {
	state := m.terminalAttach
	if state == nil || state.exited {
		return m, nil
	}
	controller, ok := m.execSessionController()
	if !ok {
		state.missing = true
		state.exited = true
		return m, nil
	}
	snapshot, found := controller.ExecSession(state.sessionID)
	if !found {
		state.missing = true
		state.exited = true
		return m, nil
	}
	state.output = snapshot.RecentOutput
	if snapshot.Status != "running" {
		state.exited = true
		if snapshot.ExitCode != nil {
			state.exitCode = *snapshot.ExitCode
		}
		return m, nil
	}
	return m, terminalAttachTickCmd()
}

// writeTerminalAttachInput forwards bytes to the session stdin. The bytes are
// written through untouched and never echoed into the transcript, a notice, or
// the overlay. Errors only update overlay state or a generic notice.
func (m model) writeTerminalAttachInput(state *terminalAttachState, data []byte) model {
	controller, ok := m.execSessionController()
	if !ok {
		state.missing = true
		state.exited = true
		return m
	}
	err := controller.WriteExecSessionInput(state.sessionID, data)
	switch {
	case errors.Is(err, execution.ErrProcessNotFound):
		state.missing = true
		state.exited = true
		state.note = "session ended"
	case errors.Is(err, execution.ErrProcessStdinDisabled):
		state.exited = true
		state.note = "session no longer accepts input"
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
	if state.exited {
		m.terminalAttach = nil
		return m, nil
	}
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
	if state.exited {
		m.terminalAttach = nil
		return m, nil
	}
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
	if !state.exited && len(tail) > 0 {
		tail[len(tail)-1] = tail[len(tail)-1] + zeroTheme.accent.Render("▌")
	}
	var footer string
	switch {
	case state.note != "":
		footer = state.note + " · press any key to close"
	case state.missing:
		footer = "session ended · press any key to close"
	case state.exited:
		footer = fmt.Sprintf("exited with code %d · press any key to close", state.exitCode)
	default:
		footer = fmt.Sprintf("type to send · ⏎ Enter · Esc detach · Ctrl+C goes to the process · /stop %d to kill", state.sessionID)
	}
	lines := []string{zeroTheme.faint.Render(command), ""}
	lines = append(lines, tail...)
	lines = append(lines, "", zeroTheme.faint.Render(footer))
	title := fmt.Sprintf("Terminal · session %d", state.sessionID)
	return centerRenderedBlock(styledBlockFillTitle(overlayWidth, title, lines, zeroTheme.lineStrong, lipgloss.NewStyle()), width)
}
