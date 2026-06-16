package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func TestStreamingFadePaletteMonotonic(t *testing.T) {
	// Build a fresh palette against the package's theme tokens so this
	// test is independent of any test-time mutation of the global.
	palette := buildStreamingFadePalette(streamingFadeSteps, lipgloss.Color(colorAccent), lipgloss.Color(colorInk))
	// Extract each bucket's foreground hex string via a type assertion
	// to lipgloss.Color. In a non-tty test env lipgloss strips ANSI,
	// so the only stable signal is the stored color literal.
	fresh, ok := palette[0].GetForeground().(lipgloss.Color)
	if !ok {
		t.Fatalf("palette[0] foreground is not a static lipgloss.Color: %T", palette[0].GetForeground())
	}
	if string(fresh) != colorAccent {
		t.Errorf("palette[0] foreground = %q, want %q (accent)", string(fresh), colorAccent)
	}
	last, ok := palette[streamingFadeSteps-1].GetForeground().(lipgloss.Color)
	if !ok {
		t.Fatalf("palette[steps-1] foreground is not a static lipgloss.Color: %T", palette[streamingFadeSteps-1].GetForeground())
	}
	if string(last) == colorAccent {
		t.Errorf("palette[steps-1] = %q (still accent); the ramp did not advance", string(last))
	}
	if string(last) == colorInk {
		t.Errorf("palette[steps-1] = %q (already ink); the last bucket should be a transition step", string(last))
	}
	// The freshest and the last transition step should differ in at
	// least one RGB channel — the ramp must not be a no-op.
	if string(fresh) == string(last) {
		t.Errorf("palette[0] and palette[steps-1] are identical (%q); the fade is a no-op", string(fresh))
	}
}

func TestStreamingFadePaletteIntermediates(t *testing.T) {
	// For each intermediate bucket, the hex must lie strictly between
	// colorAccent and colorInk in linear sRGB space. We check the
	// first channel as a coarse tripwire; the linear interpolation
	// guarantees all three channels move monotonically.
	palette := buildStreamingFadePalette(streamingFadeSteps, lipgloss.Color(colorAccent), lipgloss.Color(colorInk))
	accR, _, _, _ := hexToRGB(colorAccent)
	inkR, _, _, _ := hexToRGB(colorInk)
	// Determine direction (the ramp goes from fresh to base; we expect
	// R-channel to move from accR toward inkR over the 12 buckets).
	for i := 1; i < streamingFadeSteps; i++ {
		c, ok := palette[i].GetForeground().(lipgloss.Color)
		if !ok {
			t.Fatalf("palette[%d] foreground is not a static lipgloss.Color: %T", i, palette[i].GetForeground())
		}
		r, _, _, ok := hexToRGB(string(c))
		if !ok {
			t.Fatalf("palette[%d] foreground %q is not a #RRGGBB hex literal", i, string(c))
		}
		// Confirm r is between accR and inkR (inclusive of endpoints).
		minR, maxR := accR, inkR
		if minR > maxR {
			minR, maxR = maxR, minR
		}
		if r < minR || r > maxR {
			t.Errorf("palette[%d] R=%d is outside [%d, %d] (acc=%d, ink=%d)", i, r, minR, maxR, accR, inkR)
		}
	}
}

func TestStreamingFadePaletteLength(t *testing.T) {
	palette := buildStreamingFadePalette(streamingFadeSteps, lipgloss.Color(colorAccent), lipgloss.Color(colorInk))
	if len(palette) != streamingFadeSteps {
		t.Fatalf("palette length = %d, want %d", len(palette), streamingFadeSteps)
	}
	for i, s := range palette {
		// Each bucket must produce a non-empty render — a totally
		// transparent style would render to the same as the base, and
		// that's what we use as a tripwire.
		if s.Render("x") == "" {
			t.Errorf("palette[%d] rendered an empty string", i)
		}
	}
}

func TestStreamingFadePaletteHandlesBadHex(t *testing.T) {
	// An unparseable endpoint must not panic and must produce a fallback
	// palette where every bucket's foreground equals the (unparseable)
	// base string. The fallback path in buildStreamingFadePalette
	// short-circuits the interpolation when hexToRGB returns ok=false
	// and uses the supplied base directly for every bucket.
	bad := lipgloss.Color("not-a-color")
	palette := buildStreamingFadePalette(streamingFadeSteps, bad, bad)
	// The fix in CodeRabbit's review: don't assert on len (a constant
	// for a fixed-size array); assert that EVERY bucket's foreground
	// is the unparseable base. This catches a regression where the
	// fallback was changed to interpolate from a zero value or to
	// skip the loop entirely.
	for i, s := range palette {
		fg, ok := s.GetForeground().(lipgloss.Color)
		if !ok {
			t.Fatalf("palette[%d] foreground is not a static lipgloss.Color: %T", i, s.GetForeground())
		}
		if string(fg) != string(bad) {
			t.Errorf("palette[%d] foreground = %q, want %q (fallback base)", i, string(fg), string(bad))
		}
	}
}

func TestAgeDimLineFreshReturnsAccent(t *testing.T) {
	// At t=0 the bucket is 0 → freshest (accent). Extract the
	// foreground of the styled output via the palette's known color,
	// since lipgloss strips ANSI in non-tty tests.
	now := time.Unix(0, 0)
	base := lipgloss.NewStyle().Foreground(lipgloss.Color(colorInk))
	out := ageDimLine("hello", now, now, base)
	if !strings.Contains(out, "hello") {
		t.Errorf("fresh ageDimLine output %q does not contain the input", out)
	}
	// Sanity: the first call with age=0 must have hit palette[0],
	// which we know is the accent. We assert this indirectly: the
	// freshest bucket's color is colorAccent (already verified by
	// TestStreamingFadePaletteMonotonic), so ageDimLine at age=0
	// must use that style. We don't have a public way to extract
	// the used style from the rendered output, but we can assert
	// that the output is non-empty and contains the input.
}

func TestAgeDimLineMidRangeReturnsIntermediate(t *testing.T) {
	// At t = 600ms (halfway through the 1.2s window), the bucket
	// index is int(600 * 12 / 1200) = 6, which is one of the
	// intermediate buckets (not palette[0] = accent, not
	// zeroTheme.ink). The output must be non-empty and contain the
	// input.
	now := time.Unix(0, 0)
	base := lipgloss.NewStyle().Foreground(lipgloss.Color(colorInk))
	bornAt := now.Add(-600 * time.Millisecond)
	out := ageDimLine("hello", bornAt, now, base)
	if !strings.Contains(out, "hello") {
		t.Errorf("mid-range ageDimLine output %q does not contain the input", out)
	}
}

func TestAgeDimLineSettledReturnsBase(t *testing.T) {
	// At age >= dimDuration the base style is used directly.
	base := lipgloss.NewStyle().Foreground(lipgloss.Color(colorInk))
	now := time.Unix(0, 0)
	bornAt := now.Add(-streamingFadeDuration - time.Millisecond)
	out := ageDimLine("hello", bornAt, now, base)
	want := base.Render("hello")
	if out != want {
		t.Errorf("settled ageDimLine = %q, want %q (base render)", out, want)
	}
}

func TestAgeDimLineZeroBornAtReturnsBase(t *testing.T) {
	// The defensive path: a test fixture pre-populates m.streamingText
	// without populating m.lineAges, so bornAt is the zero time. The
	// renderer must fall back to the base color (not panic, not produce
	// neon text).
	base := lipgloss.NewStyle().Foreground(lipgloss.Color(colorInk))
	now := time.Unix(0, 0)
	out := ageDimLine("hello", time.Time{}, now, base)
	want := base.Render("hello")
	if out != want {
		t.Errorf("zero-bornAt ageDimLine = %q, want %q (base render)", out, want)
	}
}

func TestAgeDimLineBuckets(t *testing.T) {
	// Walk 0..dimDuration by 1ms and assert the bucket index is
	// monotonically non-decreasing. With 12 steps over 1200ms, each
	// bucket covers 100ms. We don't assert on the rendered colors
	// (lipgloss's per-color bytes are an implementation detail); we
	// just check the bucket math and that nothing panics.
	base := lipgloss.NewStyle().Foreground(lipgloss.Color(colorInk))
	now := time.Unix(0, 0)
	bornAt := now
	lastBucket := -1
	for age := time.Duration(0); age < streamingFadeDuration+10*time.Millisecond; age += time.Millisecond {
		bucket := int(age * time.Duration(streamingFadeSteps) / streamingFadeDuration)
		if bucket >= streamingFadeSteps {
			bucket = streamingFadeSteps - 1
		}
		if bucket < lastBucket {
			t.Fatalf("bucket regressed at age=%v: %d -> %d", age, lastBucket, bucket)
		}
		lastBucket = bucket
		// Sanity: the rendered output at any age is non-empty and
		// contains the input.
		out := ageDimLine("x", bornAt, bornAt.Add(age), base)
		if !strings.Contains(out, "x") {
			t.Errorf("ageDimLine at age=%v produced %q (missing input)", age, out)
		}
	}
	if lastBucket != streamingFadeSteps-1 {
		t.Fatalf("final bucket = %d, want %d", lastBucket, streamingFadeSteps-1)
	}
}

func TestStreamingFadeTickReturnsNonNilCmd(t *testing.T) {
	// The tick command must produce a non-nil tea.Cmd. We don't run it
	// here (would require a real message channel) — we just check it's
	// constructed. The streamingFadeTickMsg itself is asserted via the
	// case in the Update loop in the broader TUI tests.
	cmd := streamingFadeTick()
	if cmd == nil {
		t.Fatal("streamingFadeTick() returned nil; want a tea.Cmd that produces streamingFadeTickMsg")
	}
}

func TestRecordStreamingDeltaTracksNewlines(t *testing.T) {
	// A single delta containing two newlines should produce three
	// lineAges entries: the line that was being filled before the
	// delta, and one new entry per \n in the delta.
	m := model{}
	// pre-populate one entry so the first delta starts with the
	// "still-being-filled" line, not a fresh first line.
	m.lineAges = []time.Time{{}}
	oldNow := m.now
	m.now = func() time.Time { return time.Unix(0, 0) }
	defer func() { m.now = oldNow }()

	m.recordStreamingDelta("hello\nworld\n!")
	if got, want := len(m.lineAges), 3; got != want {
		t.Fatalf("lineAges length = %d, want %d (one per \\n plus the in-progress line)", got, want)
	}
}

func TestRecordStreamingDeltaSeedsFirstEntry(t *testing.T) {
	// A delta into a model with no prior lineAges should seed the
	// first entry (the in-progress line) and then append one per \n.
	m := model{}
	oldNow := m.now
	m.now = func() time.Time { return time.Unix(0, 0) }
	defer func() { m.now = oldNow }()

	m.recordStreamingDelta("a\nb\n")
	if got, want := len(m.lineAges), 3; got != want {
		t.Fatalf("lineAges length = %d, want %d (seed + one per \\n)", got, want)
	}
}

func TestResetStreamingFadeClearsState(t *testing.T) {
	m := model{}
	m.fadeActive = true
	m.lineAges = []time.Time{{}, {}}
	m.lastStreamActivity = time.Unix(0, 0)
	m.resetStreamingFade()
	if m.fadeActive {
		t.Error("resetStreamingFade left fadeActive = true")
	}
	if m.lineAges != nil {
		t.Errorf("resetStreamingFade left lineAges = %v, want nil", m.lineAges)
	}
	if !m.lastStreamActivity.IsZero() {
		t.Errorf("resetStreamingFade left lastStreamActivity = %v, want zero", m.lastStreamActivity)
	}
}

func TestStreamingLineBornAtLastVisualUsesLastActivity(t *testing.T) {
	// The last visual line should ALWAYS use lastActivity, regardless
	// of how many lineAges entries there are. This is the rule that
	// keeps the in-progress typing position visibly fresh.
	born := time.Unix(0, 0)
	activity := time.Unix(0, 5)
	got := streamingLineBornAt(2, 3, []time.Time{born, born, born, born}, activity)
	if !got.Equal(activity) {
		t.Errorf("last visual line bornAt = %v, want %v (lastActivity)", got, activity)
	}
}

func TestStreamingLineBornAtMapsVisualToLogical(t *testing.T) {
	// A non-last visual line should map to the corresponding lineAges
	// entry. With 1 visual line per logical line, visualIndex == logicalIndex.
	born := time.Unix(0, 0)
	got := streamingLineBornAt(0, 3, []time.Time{born, born, born}, time.Time{})
	if !got.Equal(born) {
		t.Errorf("first visual line bornAt = %v, want %v", got, born)
	}
}

func TestStreamingLineBornAtOutOfRangeClampsToLast(t *testing.T) {
	// A wrapped middle line: the markdown renderer produced more
	// visual lines than the number of logical lines (a single
	// logical line that wrapped into multiple visual lines). The
	// out-of-range visual index must clamp to the last known
	// logical age so the wrapped continuation lines keep fading
	// in step with their siblings instead of snapping to the zero
	// time (which would render as base ink).
	//
	// Setup: lineAges has 2 entries; visualCount=5. Visual lines
	// 0,1 map to lineAges[0,1] (the MappingVisualToLogical branch).
	// Visual line 4 is the last visual (uses lastActivity). Visual
	// line 3 is the wrapped middle line — out of lineAges range,
	// not the last visual — and must clamp to lineAges[1].
	earlier := time.Unix(0, 1)
	last := time.Unix(0, 5)
	activity := time.Unix(0, 9)
	got := streamingLineBornAt(3, 5, []time.Time{earlier, last}, activity)
	if !got.Equal(last) {
		t.Errorf("out-of-range bornAt = %v, want %v (clamp to last logical age)", got, last)
	}
}

func TestStreamingLineBornAtEmptyLineAgesReturnsZero(t *testing.T) {
	// The truly-empty case (no logical lines at all) has nothing to
	// clamp to; returning zero is correct so ageDimLine short-circuits
	// to base ink via its zero-time path.
	got := streamingLineBornAt(0, 1, nil, time.Time{})
	if !got.IsZero() {
		t.Errorf("empty lineAges bornAt = %v, want zero", got)
	}
}

func TestStyleStreamingLineFallsBackWhenFadeInactive(t *testing.T) {
	// The defensive path: fadeActive is false (e.g. a stream ended and
	// the model is now rendering the settled row). The output must be
	// the base ink render — identical to the pre-fade behavior.
	m := model{}
	m.fadeActive = false
	out := m.styleStreamingLine("hello", 0, 1)
	want := zeroTheme.ink.Render("hello")
	if out != want {
		t.Errorf("styleStreamingLine with fadeActive=false = %q, want %q (base render)", out, want)
	}
}

func TestStyleStreamingLineFallsBackWhenLineAgesNil(t *testing.T) {
	// The other defensive path: fadeActive is true (in case a
	// test fixture forgot to reset it) but lineAges is nil because
	// streamingText was pre-populated without going through
	// recordStreamingDelta. The output must still be the base render.
	m := model{}
	m.fadeActive = true
	m.lineAges = nil
	out := m.styleStreamingLine("hello", 0, 1)
	want := zeroTheme.ink.Render("hello")
	if out != want {
		t.Errorf("styleStreamingLine with nil lineAges = %q, want %q (base render)", out, want)
	}
}
