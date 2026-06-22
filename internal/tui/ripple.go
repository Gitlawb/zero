// ripple.go adds a slow cosine "breathing" colour wave for the working status
// line: each character cycles through a cold-to-warm ramp of the curated theme
// styles, and the wave travels across the text one character per phase tick. The
// phase is sourced from the shared spinner clock (m.spinnerPhase, advanced by the
// existing spinner.TickMsg handler) so the ripple and the braille spinner animate
// in lock-step with no extra ticker.
package tui

import (
	"math"
	"strings"

	"charm.land/lipgloss/v2"
)

// rippleLevel returns a palette index in [0, travel] computed from a cosine wave
// of wavelength waveLen at integer offset distance. The wave is the standard
// "breathing" half-wave-rectified cosine:
//
//	q     = distance mod waveLen
//	level = int( travel * (0.5 + 0.5*cos(2*Pi*q/waveLen)) )
//
// At distance == 0 the level is travel (brightest), at distance == waveLen/2 it
// is 0 (dimmest), and the cycle repeats every waveLen. A string rendered with
// rippleLevel(i+phase, ...) for i in [0, N) therefore shows a single cosine wave
// of colour that moves as phase advances.
//
// Edge cases:
//   - waveLen <= 0 returns 0 (no wave; flat dimmest entry)
//   - travel  <= 0 returns 0 (no palette; flat dimmest entry)
//   - distance < 0 mod-normalises to a non-negative value
//
// The function is intentionally pure: three ints in, an int out, depending only
// on the Go standard library math package — no model state, no rendering.
func rippleLevel(distance, travel, waveLen int) int {
	if travel <= 0 || waveLen <= 0 {
		return 0
	}
	// Normalise distance to [0, waveLen) so negative phases still behave: Go's %
	// can return a negative for negative inputs.
	q := distance % waveLen
	if q < 0 {
		q += waveLen
	}
	ratio := 0.5 + 0.5*math.Cos(2*math.Pi*float64(q)/float64(waveLen))
	return int(float64(travel) * ratio)
}

// rippleText applies one palette style per rune of text, using rippleLevel with
// index = i+phase so the wave travels across the string one character per phase
// tick. The palette holds curated theme styles (cold-to-warm); travel = len-1 so
// the cosine spans every entry exactly once.
//
// phase advances the wave; the caller passes m.spinnerPhase so the ripple and the
// braille spinner share a single animation clock.
//
// On an empty palette or empty text the input string is returned verbatim (no
// styling) — a safe-skip contract for callers.
func rippleText(text string, palette []lipgloss.Style, phase, waveLen int) string {
	if len(palette) == 0 || text == "" {
		return text
	}
	travel := len(palette) - 1
	if travel == 0 {
		return palette[0].Render(text)
	}

	var b strings.Builder
	for i, r := range text {
		level := rippleLevel(i+phase, travel, waveLen)
		if level < 0 {
			level = 0
		}
		if level > travel {
			level = travel
		}
		b.WriteString(palette[level].Render(string(r)))
	}
	return b.String()
}

// ripplePalette is the run-state colour ramp used by the working status line: the
// curated zeroTheme styles ordered cold (faint) at the cosine troughs to
// warm-bright (accent) at its peaks, giving a visible "the agent is thinking"
// temperature feel. Built lazily from the active theme so a /theme swap is
// reflected. No hex literal appears here — every entry is a named theme style
// (theme.go), satisfying the repo's no-hex-outside-theme rule.
func ripplePalette() []lipgloss.Style {
	return []lipgloss.Style{
		zeroTheme.faint,  // dimmest (cosine trough)
		zeroTheme.muted,  // dim
		zeroTheme.amber,  // mid (warm)
		zeroTheme.green,  // bright
		zeroTheme.accent, // brightest (cosine peak)
	}
}
