package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// The zeromaxing posture gets a LIT chip rather than coloured text.
//
// It raises a cost multiplier — 320 turns per run, inherited by every sub-agent
// — so "is it on?" must be answerable from across the room, not by reading a
// word in the same weight as everything beside it. A filled badge reads as lit
// where amber text reads as a label.
//
// THE PULSE IS FREE. It advances on the spinner tick that is already running
// while a turn is in flight, and holds a steady lit state when nothing is
// animating. ensureSpinnerTick deliberately schedules no timer on an idle
// session — "an idle plain session schedules no timer" — and a chip is not a
// reason to break that. So the glow breathes exactly while there is work to
// breathe with, and simply stays on otherwise.
//
// Reduced motion pins it to the steady state, like every other animation here.

// zeromaxingGlowFrames are the pulse's brightness steps, cycled on the spinner
// tick. Symmetric, so it breathes in and out rather than snapping back.
var zeromaxingGlowFrames = []string{"◦", "•", "●", "◉", "●", "•"}

// zeromaxingPulsePeriod is how long one full breath takes. Slow: this marks a
// standing state, not activity, and a fast blink beside a working spinner reads
// as a second thing happening.
const zeromaxingPulsePeriod = 1400 * time.Millisecond

// zeromaxingGlowChip renders the footer's posture indicator.
//
// Returns "" when the posture is off, so the footer is byte-identical without
// it — the same rule every other part of this feature follows.
func (m model) zeromaxingGlowChip() string {
	if !m.zeromaxingActive() {
		return ""
	}
	marker := m.zeromaxingPulseGlyph()
	body := " " + marker + " " + zeromaxingChipLabel + " "
	// HOVER: the chip is clickable — it opens /effort — so it has to say so
	// under the cursor. Without a hover state a clickable chip is
	// indistinguishable from a label, and the user never learns it can be
	// pressed.
	if m.hover.kind == hoverZeromaxingChip {
		return zeroTheme.hover.Render(body)
	}
	// A filled badge: the label sits ON the amber rather than in it, which is
	// what makes it read as lit rather than as another word in the row.
	return zeroTheme.permBadge.Render(body)
}

// zeromaxingChipWidth is the chip's rendered cell width, used to hit-test it.
// Derived from the same string the renderer builds, so the two cannot drift.
func zeromaxingChipWidth() int {
	return len([]rune(" ● " + zeromaxingChipLabel + " "))
}

// zeromaxingChipAtMouse reports whether the cursor is over the footer chip.
//
// The chip sits at the END of the footer's left run, so its span is measured
// from the rendered footer rather than assumed: the chips before it (permission
// mode, effort) vary in width with the session.
func (m model) zeromaxingChipAtMouse(msg tea.MouseMsg) bool {
	// A FAST PATH, not the enforcement: with the posture off the footer carries
	// no chip, so the span lookup below fails anyway. Removing this guard does
	// not make the chip hittable — it just renders the footer to find that out.
	if !m.zeromaxingActive() || !m.altScreen || m.height <= 0 {
		return false
	}
	row, ok := m.zeromaxingChipRow()
	if !ok || mouseY(msg) != row {
		return false
	}
	start, end, ok := m.zeromaxingChipSpan()
	if !ok {
		return false
	}
	x := mouseX(msg)
	return x >= start && x < end
}

// zeromaxingChipRow is the footer row the chip renders on.
func (m model) zeromaxingChipRow() (int, bool) {
	footer := viewLines(m.footerView(m.chatColumnWidth()))
	for index, line := range footer {
		if strings.Contains(ansiStripLine(line), zeromaxingChipLabel) {
			// Footer rows sit at the bottom of the screen.
			return m.height - len(footer) + index, true
		}
	}
	return 0, false
}

// zeromaxingChipSpan is the chip's [start,end) column range on its row.
func (m model) zeromaxingChipSpan() (int, int, bool) {
	footer := viewLines(m.footerView(m.chatColumnWidth()))
	for _, line := range footer {
		plain := ansiStripLine(line)
		index := strings.Index(plain, zeromaxingChipLabel)
		if index < 0 {
			continue
		}
		// The label is preceded by " ● " inside the badge.
		start := maxInt(0, index-3)
		return start, start + zeromaxingChipWidth(), true
	}
	return 0, 0, false
}

// zeromaxingPulseGlyph picks the current breath frame. Steady when nothing is
// animating, so the chip never freezes mid-pulse on a dimmed frame — a half-lit
// chip on an idle session would read as a rendering fault.
func (m model) zeromaxingPulseGlyph() string {
	if m.reducedMotion || !m.pending {
		return "●"
	}
	step := m.now().UnixMilli() % zeromaxingPulsePeriod.Milliseconds()
	index := int(step * int64(len(zeromaxingGlowFrames)) / zeromaxingPulsePeriod.Milliseconds())
	if index < 0 || index >= len(zeromaxingGlowFrames) {
		return "●"
	}
	return zeromaxingGlowFrames[index]
}

// zeromaxingOptionValue is the posture's name as it appears in option lists.
// Declared here rather than reaching into execprofile so this file has no
// dependency beyond rendering.
const zeromaxingOptionValue = "zeromaxing"

// ansiStripLine removes styling so a rendered row can be measured in cells.
func ansiStripLine(line string) string { return ansi.Strip(line) }

// isZeromaxingOption reports whether a picker/palette entry is the posture.
func isZeromaxingOption(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), zeromaxingOptionValue)
}
