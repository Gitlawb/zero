package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
)

// leaderTimeout is how long the leader prefix waits for a follow-up key before
// the chord cancels itself. Short enough to not feel sticky; long enough to
// find the second key without racing.
const leaderTimeout = 2 * time.Second

// leaderExpiredMsg clears leader-pending when the follow-up window elapses.
// seq must match m.leaderSeq or the tick is stale (a later leader re-armed).
type leaderExpiredMsg struct {
	seq int
}

func (m model) canArmLeader() bool {
	return m.noBlockingModal() && !m.transcriptDetailed && !m.subchat.active && !m.helpOverlay && !m.leaderHelpOverlay
}

func (m model) armLeader() (model, tea.Cmd) {
	m.leaderPending = true
	m.leaderSeq++
	seq := m.leaderSeq
	return m, tea.Tick(leaderTimeout, func(time.Time) tea.Msg {
		return leaderExpiredMsg{seq: seq}
	})
}

func (m model) clearLeader() model {
	m.leaderPending = false
	// Bump seq so an in-flight timeout tick becomes a no-op.
	m.leaderSeq++
	return m
}

// handleLeaderKey consumes the key while leader-pending is armed. Never inserts
// into the composer; either runs a mapped slash command, opens the chord map
// (?), or cancels the chord. Ctrl+C is handled before this is called so
// exit/cancel still works.
func (m model) handleLeaderKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Second press of the same leader key cancels (does not re-arm).
	if m.matchesLeaderKey(msg) {
		return m.clearLeader(), nil
	}
	if keyEsc(msg) {
		return m.clearLeader(), nil
	}
	// Leader + ? opens the full leader-chord map. Not a letter binding, so
	// handle before leaderSecondKey.
	if keyText(msg) == "?" && !keyAlt(msg) && !keyHasMod(msg, tea.ModCtrl) {
		m = m.clearLeader()
		m.leaderHelpOverlay = true
		return m, nil
	}
	key, ok := leaderSecondKey(msg)
	if !ok {
		return m.clearLeader(), nil
	}
	slash, mapped := m.leaderCommands[key]
	if !mapped {
		return m.clearLeader(), nil
	}
	m = m.clearLeader()
	return m.executeSlash(slash)
}

// matchesLeaderKey reports whether msg is the resolved leader prefix.
func (m model) matchesLeaderKey(msg tea.KeyMsg) bool {
	if !m.leaderKey.isZero() {
		return m.leaderKey.Matcher()(msg)
	}
	return keyCtrl(msg, 'x')
}

// leaderPendingHint is the faint status line while a leader chord is armed.
// Examples come from the resolved leaderCommands map so remaps/unbindings show
// correctly; ? list and Esc cancel stay fixed.
func (m model) leaderPendingHint() string {
	label := m.leaderKeyLabel()
	examples := leaderPendingExamples(m.leaderCommands, 2)
	if len(examples) == 0 {
		return fmt.Sprintf("%s — await shortcut (? list · Esc cancel)", label)
	}
	return fmt.Sprintf("%s — await shortcut (%s · ? list · Esc cancel)", label, strings.Join(examples, " · "))
}

// leaderPendingExamples returns up to n "letter name" snippets from commands,
// sorted the same way as the leader help table (stable, lowercase-first).
func leaderPendingExamples(commands map[rune]string, n int) []string {
	if n <= 0 || len(commands) == 0 {
		return nil
	}
	letters := make([]rune, 0, len(commands))
	for r := range commands {
		letters = append(letters, r)
	}
	sort.Slice(letters, func(i, j int) bool {
		a, b := letters[i], letters[j]
		al, bl := unicode.ToLower(a), unicode.ToLower(b)
		if al != bl {
			return al < bl
		}
		return a < b
	})
	if len(letters) > n {
		letters = letters[:n]
	}
	out := make([]string, 0, len(letters))
	for _, r := range letters {
		slash := commands[r]
		name := strings.TrimPrefix(slash, "/")
		if name == "" {
			name = slash
		}
		out = append(out, fmt.Sprintf("%c %s", r, name))
	}
	return out
}

// leaderHelpBindings builds the display-ordered table of every assigned leader
// chord from the resolved map.
func leaderHelpBindings(label string, commands map[rune]string) []keybinding {
	if label == "" {
		label = "Ctrl+X"
	}
	letters := make([]rune, 0, len(commands))
	for r := range commands {
		letters = append(letters, r)
	}
	sort.Slice(letters, func(i, j int) bool {
		a, b := letters[i], letters[j]
		// Lowercase before uppercase for the same base; then by rune.
		al, bl := unicode.ToLower(a), unicode.ToLower(b)
		if al != bl {
			return al < bl
		}
		return a < b
	})
	out := make([]keybinding, 0, len(letters)+1)
	for _, r := range letters {
		slash := commands[r]
		out = append(out, keybinding{
			keys: fmt.Sprintf("%s %c", label, r),
			desc: leaderHelpDesc(slash),
		})
	}
	out = append(out, keybinding{
		keys: fmt.Sprintf("%s ?", label),
		desc: "show this list",
	})
	return out
}

func leaderHelpDesc(slash string) string {
	switch slash {
	case "/model", "/provider", "/stt-model", "/resume", "/image":
		return "open " + slash
	case "/voice":
		return "toggle " + slash
	default:
		return "run " + slash
	}
}

func leaderHelpFooter(label string) string {
	if label == "" {
		label = "Ctrl+X"
	}
	return "? or Esc to close \u00b7 letter after " + label + " runs the command"
}

// renderLeaderHelpLines builds the body of the leader ? overlay — same shape as
// renderKeybindingHelpLines so the two modals share layout and column alignment.
func (m model) renderLeaderHelpLines(innerWidth int) []string {
	label := m.leaderKeyLabel()
	groups := []keybindingGroup{{
		title:    "Slash commands",
		bindings: leaderHelpBindings(label, m.leaderCommands),
	}}
	keyColumn := keybindingKeyColumnWidth(groups)
	lines := make([]string, 0, 32)
	for index, group := range groups {
		if index > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, zeroTheme.accent.Render(group.title))
		for _, binding := range group.bindings {
			lines = append(lines, formatKeybindingLine(binding, keyColumn, innerWidth))
		}
	}
	lines = append(lines, "")
	lines = append(lines, zeroTheme.faint.Render(leaderHelpFooter(label)))
	return lines
}

// renderLeaderHelpOverlay frames the leader chord map like the general ?
// keyboard-shortcut overlay.
func (m model) renderLeaderHelpOverlay(width int) string {
	overlayWidth := keybindingHelpOverlayWidth(width)
	lines := m.renderLeaderHelpLines(overlayWidth - 4)
	title := m.leaderKeyLabel() + " Shortcuts"
	block := styledBlockFillTitle(overlayWidth, title, lines, zeroTheme.line, zeroTheme.panel)
	return centerRenderedBlock(block, width)
}

// leaderSecondKey extracts the letter for a leader follow-up. Prefers printable
// text ("m" / "M"); falls back to code+Shift so test harnesses and terminals
// that report only ModShift still work.
func leaderSecondKey(msg tea.KeyMsg) (rune, bool) {
	if keyHasMod(msg, tea.ModCtrl) || keyAlt(msg) {
		return 0, false
	}
	text := keyText(msg)
	if runes := []rune(text); len(runes) == 1 && unicode.IsLetter(runes[0]) {
		return runes[0], true
	}
	code := keyCode(msg)
	if code >= 'a' && code <= 'z' {
		if keyShift(msg) {
			return code - ('a' - 'A'), true
		}
		return code, true
	}
	if code >= 'A' && code <= 'Z' {
		return code, true
	}
	return 0, false
}
