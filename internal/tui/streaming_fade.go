package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Streaming-text age-based fade.
//
// While the assistant reply streams in, the latest line appears in the brand
// lime ("fresh") and gradually settles to the standard off-white ink over
// ~1.2 seconds. The effect mirrors the lime spinner glyph in the liveness
// block: it's a quiet visual signal that the model is still emitting, not a
// stylized "neon glow". Per-line granularity by design — per-rune would be
// more correct for single-token streams but is ~10x the work and visually
// indistinguishable for prose.
//
// The palette is pre-computed at init() in linear sRGB. Both endpoints are
// near-white (accent #caff3f and ink #ececee), so the perceptual difference
// between sRGB and CIELAB interpolation is invisible; we save the Lab math
// (and the go-colorful dependency) for the day someone complains.

const (
	// streamingFadeSteps is the number of discrete buckets in the age→color
	// ramp. 12 is the sweet spot: smooth at 150ms cadence, cheap to look up,
	// no per-frame float math.
	streamingFadeSteps = 12

	// streamingFadeDuration is how long a freshly-streamed line takes to
	// settle from the brand-lime "fresh" color to the off-white "ink" base.
	// 1.2s is the minimum that reads as a deliberate fade instead of a
	// flicker — the eye registers the change and the text is already solid
	// before the user re-reads it.
	streamingFadeDuration = 1200 * time.Millisecond

	// streamingFadeTickInterval is the cadence at which we re-render the
	// fading text. Independent of the 80ms spinner tick — a slower cadence
	// is enough for a smooth-feeling fade and keeps the per-frame work
	// cheap.
	streamingFadeTickInterval = 150 * time.Millisecond
)

// streamingFadeTickMsg re-renders the streaming-text fade on the next frame.
// The Update loop schedules the FOLLOWING tick in the case branch so the
// ticker self-perpetuates while fadeActive is true.
type streamingFadeTickMsg time.Time

// streamingFadePalette holds the 12-step ramp from fresh (accent) to settled
// (ink), pre-computed at init() in linear sRGB. Index 0 = freshest, index
// N-1 = closest to settled. The styles are stored eagerly (one
// lipgloss.NewStyle call per step) so per-frame lookups are a struct-field
// access and a single Render call, not a hex parse.
var streamingFadePalette [streamingFadeSteps]lipgloss.Style

// init builds the palette once at package load. Cheap; called before any
// model is constructed.
func init() {
	streamingFadePalette = buildStreamingFadePalette(
		streamingFadeSteps,
		lipgloss.Color(colorAccent),
		lipgloss.Color(colorInk),
	)
}

// buildStreamingFadePalette interpolates `steps` colors from fresh to base
// in linear sRGB (each channel independently). Exposed for tests so they can
// build a 1-step or 3-step palette without the package-init side effect.
func buildStreamingFadePalette(steps int, fresh, base lipgloss.Color) [streamingFadeSteps]lipgloss.Style {
	var palette [streamingFadeSteps]lipgloss.Style
	if steps < 1 {
		steps = 1
	}
	if steps > streamingFadeSteps {
		steps = streamingFadeSteps
	}
	freshR, freshG, freshB, ok := hexToRGB(string(fresh))
	if !ok {
		// Theme didn't have a #RRGGBB literal (shouldn't happen — all our
		// theme tokens are 7-char hex). Fall back to a no-op palette so
		// streamed text stays in the base color rather than going neon.
		for i := 0; i < steps; i++ {
			palette[i] = lipgloss.NewStyle().Foreground(base)
		}
		return palette
	}
	baseR, baseG, baseB, ok := hexToRGB(string(base))
	if !ok {
		for i := 0; i < steps; i++ {
			palette[i] = lipgloss.NewStyle().Foreground(base)
		}
		return palette
	}
	// 12 steps over a 1.2s window ≈ 100ms per step, so each step boundary
	// is well below the eye's flicker fusion threshold. Index 0 is the
	// accent (freshest); index steps-1 is one step away from ink.
	for i := 0; i < steps; i++ {
		t := float64(i) / float64(steps)
		r := uint8(float64(freshR) + t*(float64(baseR)-float64(freshR)))
		g := uint8(float64(freshG) + t*(float64(baseG)-float64(freshG)))
		b := uint8(float64(freshB) + t*(float64(baseB)-float64(freshB)))
		palette[i] = lipgloss.NewStyle().Foreground(lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", r, g, b)))
	}
	return palette
}

// hexToRGB parses a 7-character "#RRGGBB" string into 0-255 RGB channels.
// Returns ok=false on any other shape; the caller falls back to a no-op
// palette. We don't reach for a general hex parser because every theme
// token in this codebase is the 7-char form.
func hexToRGB(s string) (uint8, uint8, uint8, bool) {
	if len(s) != 7 || s[0] != '#' {
		return 0, 0, 0, false
	}
	hex := func(c byte) (uint8, bool) {
		switch {
		case c >= '0' && c <= '9':
			return c - '0', true
		case c >= 'a' && c <= 'f':
			return c - 'a' + 10, true
		case c >= 'A' && c <= 'F':
			return c - 'A' + 10, true
		default:
			return 0, false
		}
	}
	hi, ok1 := hex(s[1])
	lo, ok2 := hex(s[2])
	if !ok1 || !ok2 {
		return 0, 0, 0, false
	}
	r := hi*16 + lo
	hi, ok1 = hex(s[3])
	lo, ok2 = hex(s[4])
	if !ok1 || !ok2 {
		return 0, 0, 0, false
	}
	g := hi*16 + lo
	hi, ok1 = hex(s[5])
	lo, ok2 = hex(s[6])
	if !ok1 || !ok2 {
		return 0, 0, 0, false
	}
	b := hi*16 + lo
	return r, g, b, true
}

// streamingFadeTick returns the next tea.Tick command. The Update loop
// schedules the FOLLOWING tick in the streamingFadeTickMsg case so the
// ticker self-perpetuates while fadeActive is true (and stops cleanly when
// the stream ends, since the case short-circuits to nil).
func streamingFadeTick() tea.Cmd {
	return tea.Tick(streamingFadeTickInterval, func(t time.Time) tea.Msg {
		return streamingFadeTickMsg(t)
	})
}

// ageDimLine returns `line` styled with the color bucket corresponding to
// `bornAt`'s age relative to `now`. When `bornAt.IsZero()` (no age recorded
// — test fixtures, or a stream that just started), returns `line` styled
// with `base` so direct-fixture tests and the very first frame render
// identically to the pre-fade behavior.
//
// Per-line, not per-rune, by design: cheaper, visually equivalent for
// streaming prose, and survives soft-wrap without grapheme-segmentation
// code. The cost is one palette lookup and one Render call per visible line
// per frame.
func ageDimLine(line string, bornAt, now time.Time, base lipgloss.Style) string {
	if bornAt.IsZero() {
		return base.Render(line)
	}
	age := now.Sub(bornAt)
	if age < 0 {
		// Clock skew (e.g. the test fixture hand-codes bornAt in the
		// future). Treat as freshest.
		age = 0
	}
	if age >= streamingFadeDuration {
		return base.Render(line)
	}
	// Map age in [0, dimDuration) to bucket in [0, steps). With 12 steps
	// over 1.2s the bucket width is exactly 100ms, so age = 0 → bucket 0,
	// age = 99ms → bucket 0, age = 100ms → bucket 1, …
	bucket := int(age * time.Duration(streamingFadeSteps) / streamingFadeDuration)
	if bucket >= streamingFadeSteps {
		// Floating-point edge: age = dimDuration - 1ns could still land
		// here after the int truncation. Clamp.
		bucket = streamingFadeSteps - 1
	}
	return streamingFadePalette[bucket].Render(line)
}

// streamingLineBornAt looks up the bornAt for a visual line in the streaming
// block. `lineAges` is keyed to LOGICAL lines (one entry per `\n` in
// `m.streamingText`); the markdown renderer may wrap a single logical line
// into multiple VISUAL lines, so we have to disambiguate.
//
// `visualIndex` is the index into the rendered `lines` slice; `visualCount`
// is the total visual-line count. `lastActivity` is the timestamp of the
// most recent delta and is used for the in-progress last visual line — the
// user can see exactly where the model is currently typing, so this is the
// "freshest" line by definition.
//
// The mapping rule:
//   - The last VISUAL line uses `lastActivity` (always freshest).
//   - All other visual lines use the corresponding entry in `lineAges`.
//     A single logical line that wrapped into N visual lines uses the same
//     `lineAges[k]` for all N of them — they're the same age, just wrapped.
//
// Returns time.Time{} when no age can be determined (test fixtures that
// pre-populate streamingText without lineAges); ageDimLine short-circuits
// that case to the base color.
func streamingLineBornAt(visualIndex, visualCount int, lineAges []time.Time, lastActivity time.Time) time.Time {
	if visualCount == 0 {
		return time.Time{}
	}
	if visualIndex == visualCount-1 {
		// The in-progress last line is always freshest by construction.
		return lastActivity
	}
	// Map visual index to logical index. We don't have the wrap map here
	// (the markdown renderer doesn't return one), so we use a simple
	// approximation: assume 1 visual line per logical line, which is
	// exact for lines that don't wrap and conservative for lines that do.
	// The visual line numbering is dominated by lines that don't wrap,
	// so a one-line-at-a-time mapping is correct for the majority case.
	if visualIndex < 0 || visualIndex >= len(lineAges) {
		return time.Time{}
	}
	return lineAges[visualIndex]
}

// recordStreamingDelta updates the fade state after a streaming delta
// arrives. Appends a time.Time entry to lineAges for every newline in the
// delta (so the new line that just started has its own age) and updates
// lastActivity to now (so the in-progress last line stays fresh).
//
// Called from the agentTextMsg branch; the same now-time is used for every
// entry appended in one delta, which is fine — deltas are typically <1ms.
func (m *model) recordStreamingDelta(delta string) {
	now := m.now()
	if m.lineAges == nil {
		m.lineAges = []time.Time{now}
	} else {
		// Update the trailing entry's age to `now` so the line that's
		// still being filled stays fresh. The first append below (for
		// any newline in the delta) re-bumps it.
		m.lineAges[len(m.lineAges)-1] = now
	}
	for _, r := range delta {
		if r == '\n' {
			m.lineAges = append(m.lineAges, now)
		}
	}
	m.lastStreamActivity = now
}

// resetStreamingFade clears the fade state. Called on stream end (so the
// next turn starts from a clean slate) and on cancel (so the partial
// stream doesn't leave dangling state).
func (m *model) resetStreamingFade() {
	m.fadeActive = false
	m.lineAges = nil
	m.lastStreamActivity = time.Time{}
}

// styleStreamingLine applies the fade palette to one visual line of the
// streaming block. When fadeActive is false (or lineAges is nil because a
// test fixture pre-populated streamingText), the line is styled with the
// base ink color — the same look the streaming block had before the fade
// feature shipped.
func (m model) styleStreamingLine(line string, visualIndex, visualCount int) string {
	if !m.fadeActive || m.lineAges == nil {
		return zeroTheme.ink.Render(line)
	}
	bornAt := streamingLineBornAt(visualIndex, visualCount, m.lineAges, m.lastStreamActivity)
	return ageDimLine(line, bornAt, m.now(), zeroTheme.ink)
}

// ensureAgeTickReschedule is a small helper used after a fade-state change
// to start the tick if it's not already running. The age-tick case
// short-circuits when fadeActive is false, so calling this on a no-op
// transition (e.g. a 0-byte delta) is safe.
func (m model) ensureAgeTickReschedule() tea.Cmd {
	if !m.fadeActive {
		return nil
	}
	return streamingFadeTick()
}
