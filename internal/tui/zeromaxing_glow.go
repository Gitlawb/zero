package tui

import (
	"strings"
	"time"
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
	// A filled badge: the label sits ON the amber rather than in it, which is
	// what makes it read as lit rather than as another word in the row.
	return zeroTheme.permBadge.Render(" " + marker + " " + zeromaxingChipLabel + " ")
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

// isZeromaxingOption reports whether a picker/palette entry is the posture.
func isZeromaxingOption(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), zeromaxingOptionValue)
}
