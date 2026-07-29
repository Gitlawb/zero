package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func glowModel(t *testing.T) model {
	t.Helper()
	m := model{now: func() time.Time { return time.Unix(1000, 0) }}
	m.execProfileName = "zeromaxing"
	m.zeromaxing = 2 // ZeromaxingActive
	if !m.zeromaxingActive() {
		t.Fatal("setup: the posture must be active")
	}
	return m
}

// The chip is LIT, not merely coloured: a filled badge reads as on from across
// the room, where amber text reads as another word in the row. The posture
// raises a cost multiplier, so "is it on?" has to be answerable at a glance.
//
// Asserted on the RENDERED FOOTER, not on the helper: the first version called
// zeromaxingGlowChip directly, and reverting the footer to plain amber text
// passed it.
func TestTheZeromaxingChipIsAFilledBadge(t *testing.T) {
	m := glowModel(t)
	m.width, m.height = 100, 30

	footer := m.footerView(m.width)
	if !strings.Contains(ansi.Strip(footer), zeromaxingChipLabel) {
		t.Fatalf("the footer does not carry the posture label:\n%s", ansi.Strip(footer))
	}
	// A background fill on the label's own run is what distinguishes a lit
	// badge from coloured text.
	if !strings.Contains(footer, "48;2;") {
		t.Fatalf("the footer chip has no background fill, so it is text and not a badge")
	}
	if chip := m.zeromaxingGlowChip(); !strings.Contains(footer, chip) {
		t.Fatalf("the footer does not render the glow chip it was given:\n%s", ansi.Strip(footer))
	}
}

// Off means ABSENT, not dim. The footer is byte-identical without the posture,
// which is the rule every part of this feature follows.
func TestThePostureChipVanishesWhenOff(t *testing.T) {
	m := model{now: func() time.Time { return time.Unix(1000, 0) }}
	if got := m.zeromaxingGlowChip(); got != "" {
		t.Fatalf("with the posture off the chip must render nothing, got %q", got)
	}
}

// THE PULSE IS FREE, and that constraint shapes it: it advances on the spinner
// tick that is already running during a turn, and holds steady otherwise.
// ensureSpinnerTick schedules no timer on an idle session, and a chip is not a
// reason to break that.
func TestThePulseOnlyBreathesWhileThereIsWork(t *testing.T) {
	m := glowModel(t)

	// Idle: steady, at full brightness. A chip frozen on a dim frame would read
	// as a rendering fault.
	for _, ms := range []int64{0, 300, 700, 1100} {
		m.now = func() time.Time { return time.UnixMilli(ms) }
		if got := m.zeromaxingPulseGlyph(); got != "●" {
			t.Fatalf("idle at %dms rendered %q, want a steady full glyph", ms, got)
		}
	}

	// Pending: it moves.
	m.pending = true
	seen := map[string]bool{}
	for ms := int64(0); ms < zeromaxingPulsePeriod.Milliseconds(); ms += 100 {
		m.now = func() time.Time { return time.UnixMilli(ms) }
		seen[m.zeromaxingPulseGlyph()] = true
	}
	if len(seen) < 3 {
		t.Fatalf("the pulse only reached %d frames across a full period; it is not breathing", len(seen))
	}
}

// Reduced motion pins it, like every other animation here.
func TestReducedMotionStopsThePulse(t *testing.T) {
	m := glowModel(t)
	m.pending = true
	m.reducedMotion = true
	for _, ms := range []int64{0, 400, 900} {
		m.now = func() time.Time { return time.UnixMilli(ms) }
		if got := m.zeromaxingPulseGlyph(); got != "●" {
			t.Fatalf("reduced motion still animated: %q at %dms", got, ms)
		}
	}
}

// The pulse never falls off its frame table, whatever the clock says.
func TestThePulseNeverLeavesItsFrames(t *testing.T) {
	m := glowModel(t)
	m.pending = true
	valid := map[string]bool{}
	for _, frame := range zeromaxingGlowFrames {
		valid[frame] = true
	}
	valid["●"] = true
	for _, ms := range []int64{0, 1, 1399, 1400, 999999999, 1} {
		m.now = func() time.Time { return time.UnixMilli(ms) }
		if got := m.zeromaxingPulseGlyph(); !valid[got] {
			t.Fatalf("clock %dms produced %q, which is not a frame", ms, got)
		}
	}
}

// The posture is marked where it is OFFERED too, so what you are about to turn
// on looks like what you will see once it is on. Marked rather than merely
// coloured: the selected picker row already owns its background, so colour
// alone would be invisible exactly when you are looking at it.
func TestThePostureIsMarkedInTheEffortPicker(t *testing.T) {
	for _, name := range []string{"glm-5.2", "claude-sonnet-4.5", "gpt-4o"} {
		var postureLabel string
		for _, item := range (model{modelName: name}).newEffortPicker().items {
			if isZeromaxingOption(item.Value) {
				postureLabel = item.Label
			}
		}
		if postureLabel == "" {
			t.Fatalf("%s: the picker does not offer the posture at all", name)
		}
		if !strings.Contains(postureLabel, "◉") {
			t.Errorf("%s: the posture row is unmarked: %q", name, postureLabel)
		}
	}
}

// The marker is display only — the VALUE the picker hands to the command must
// stay the bare name, or selecting it would be refused as an unknown effort.
func TestTheMarkerDoesNotLeakIntoTheSelectedValue(t *testing.T) {
	m := model{modelName: "glm-5.2"}
	// Located by its LABEL, not its value: matching on the value would skip the
	// row entirely the moment the marker leaked into it, and the test would
	// pass by finding nothing.
	found := false
	for _, item := range m.newEffortPicker().items {
		if !strings.Contains(item.Label, "◉") {
			continue
		}
		found = true
		if item.Value != "zeromaxing" {
			t.Fatalf("the picker would send %q to the command, want the bare name", item.Value)
		}
		if _, out := m.handleEffortCommand(item.Value); strings.Contains(out, "Unknown reasoning effort") {
			t.Fatalf("the command refuses the value the picker offers: %s", out)
		}
	}
	if !found {
		t.Fatal("no marked row in the picker at all")
	}
}

// HOVER ON THE CHIP. It opens /effort when pressed, so it has to say so under
// the cursor — a clickable chip that looks identical to a label never teaches
// anyone it can be pressed.
func TestTheChipHighlightsUnderTheCursor(t *testing.T) {
	m := glowModel(t)
	m.width, m.height = 100, 30
	m.altScreen = true

	plain := m.zeromaxingGlowChip()
	m.hover = hoverTarget{kind: hoverZeromaxingChip}
	hovered := m.zeromaxingGlowChip()

	if plain == hovered {
		t.Fatal("the chip renders identically hovered and not, so hovering it says nothing")
	}
	if !strings.Contains(ansi.Strip(hovered), zeromaxingChipLabel) {
		t.Fatalf("the hovered chip lost its label: %q", ansi.Strip(hovered))
	}
}

// The chip's hit region is measured from the RENDERED footer, not assumed: the
// chips before it vary in width with the session's permission mode and effort.
func TestTheChipHitRegionTracksTheRenderedFooter(t *testing.T) {
	m := glowModel(t)
	m.width, m.height = 100, 30
	m.altScreen = true

	start, end, ok := m.zeromaxingChipSpan()
	if !ok {
		t.Fatal("the chip span could not be resolved from the footer")
	}
	if end <= start {
		t.Fatalf("empty chip span [%d,%d)", start, end)
	}
	row, ok := m.zeromaxingChipRow()
	if !ok {
		t.Fatal("the chip row could not be resolved")
	}

	inside := tea.MouseMotionMsg{X: start + 1, Y: row}
	if !m.zeromaxingChipAtMouse(inside) {
		t.Fatalf("a point inside the chip [%d,%d) on row %d did not hit", start, end, row)
	}
	for _, outside := range []tea.MouseMotionMsg{
		{X: maxInt(0, start-2), Y: row},
		{X: end + 2, Y: row},
		{X: start + 1, Y: maxInt(0, row-2)},
	} {
		if m.zeromaxingChipAtMouse(outside) {
			t.Fatalf("a point outside the chip (%d,%d) hit anyway", outside.X, outside.Y)
		}
	}
}

// With the posture off there is no chip, so nothing can hover or click it.
func TestTheChipIsNotHittableWhenThePostureIsOff(t *testing.T) {
	m := model{now: func() time.Time { return time.Unix(1000, 0) }}
	m.width, m.height = 100, 30
	m.altScreen = true
	if m.zeromaxingChipAtMouse(tea.MouseMotionMsg{X: 5, Y: 29}) {
		t.Fatal("the chip is hittable with the posture off")
	}
}

// Clicking it opens the effort picker — where the posture can be turned off or
// changed.
func TestClickingTheChipOpensTheEffortPicker(t *testing.T) {
	m := glowModel(t)
	m.width, m.height = 100, 30
	m.altScreen = true
	m.modelName = "glm-5.2"

	row, ok := m.zeromaxingChipRow()
	if !ok {
		t.Fatal("the chip row could not be resolved")
	}
	start, _, _ := m.zeromaxingChipSpan()

	updated, _ := m.Update(tea.MouseClickMsg{X: start + 1, Y: row, Button: tea.MouseLeft})
	m = updated.(model)
	if m.picker == nil || m.picker.kind != pickerEffort {
		t.Fatalf("clicking the chip did not open the effort picker: %#v", m.picker)
	}
}

// ...but not mid-turn: /effort refuses while a run is in flight, and a dialog
// that cannot be acted on is worse than none.
func TestClickingTheChipMidTurnOpensNothing(t *testing.T) {
	m := glowModel(t)
	m.width, m.height = 100, 30
	m.altScreen = true
	m.pending = true

	row, _ := m.zeromaxingChipRow()
	start, _, _ := m.zeromaxingChipSpan()
	updated, _ := m.Update(tea.MouseClickMsg{X: start + 1, Y: row, Button: tea.MouseLeft})
	if updated.(model).picker != nil {
		t.Fatal("a picker opened mid-turn, where it cannot be acted on")
	}
}
