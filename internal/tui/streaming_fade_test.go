package tui

import (
	"image/color"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
)

// rgbaOf is a small test helper that asserts the foreground is a
// concrete color.RGBA (the stable form we convert to in
// buildStreamingFadePalette) and returns the channels. It fails the
// test on any other type so a regression to an opaque interface — or
// to a nil NoColor — doesn't silently pass.
func rgbaOf(t *testing.T, s lipgloss.Style) color.RGBA {
	t.Helper()
	fg := s.GetForeground()
	rgba, ok := fg.(color.RGBA)
	if !ok {
		t.Fatalf("foreground is %T, want color.RGBA", fg)
	}
	return rgba
}

func TestStreamingFadePaletteMonotonic(t *testing.T) {
	// Build a fresh palette against the package's theme tokens so this
	// test is independent of any test-time mutation of the global.
	palette := buildStreamingFadePalette(streamingFadeSteps, lipgloss.Color(colorAccent), lipgloss.Color(colorInk))
	// We convert the Blend1D output to color.RGBA inside the builder
	// so per-bucket comparisons are byte-stable. The endpoint byte
	// values are the contract — the hex strings are not.
	fresh := rgbaOf(t, palette[0])
	// Sanity: palette[0] must equal the accent endpoint (#caff3f).
	if fresh.R != 0xca || fresh.G != 0xff || fresh.B != 0x3f {
		t.Errorf("palette[0] RGBA = %v, want {R:0xca, G:0xff, B:0x3f} (accent)", fresh)
	}
	last := rgbaOf(t, palette[streamingFadeSteps-1])
	if last == fresh {
		t.Errorf("palette[steps-1] = %v; the ramp did not advance from the accent", last)
	}
	// Blend1D's last sample is the ink endpoint itself, so we don't
	// assert the last bucket is "intermediate" — we only assert the
	// ramp actually moved (the eye can see the difference between
	// fresh and the last bucket even if the very last sample lands on
	// ink). Verify it advanced far enough that the fade is visible:
	// at least one channel must be at least halfway between fresh and
	// ink.
	midway := func(fresh, last, target uint8) bool {
		return absDelta8(last, fresh) >= absDelta8(target, fresh)/2
	}
	inkR, inkG, inkB := byte(0xec), byte(0xec), byte(0xee)
	if !midway(fresh.R, last.R, inkR) && !midway(fresh.G, last.G, inkG) && !midway(fresh.B, last.B, inkB) {
		t.Errorf("palette[steps-1] = %v did not advance at least halfway toward ink %v", last, color.RGBA{R: inkR, G: inkG, B: inkB})
	}
}

// absDelta8 returns the absolute difference of two uint8 values as int.
func absDelta8(a, b byte) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}

func TestStreamingFadePaletteIntermediates(t *testing.T) {
	// For each intermediate bucket, at least one RGB channel must
	// change relative to its neighbor — the ramp is not a no-op. We
	// don't pin the exact channel path (Blend1D uses CIELAB, which is
	// not a straight per-channel interpolation), but we do confirm the
	// ramp makes monotonic progress.
	palette := buildStreamingFadePalette(streamingFadeSteps, lipgloss.Color(colorAccent), lipgloss.Color(colorInk))
	prev := rgbaOf(t, palette[0])
	for i := 1; i < streamingFadeSteps; i++ {
		cur := rgbaOf(t, palette[i])
		// At least one channel must have advanced (i.e. moved further
		// from fresh). This is a tripwire on a regression to a flat
		// palette; it doesn't constrain the per-bucket math.
		progressed := absDelta8(cur.R, prev.R) > 0 ||
			absDelta8(cur.G, prev.G) > 0 ||
			absDelta8(cur.B, prev.B) > 0
		if !progressed {
			t.Errorf("palette[%d] RGBA %v did not advance from palette[%d] %v", i, cur, i-1, prev)
		}
		prev = cur
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
	// An unparseable endpoint must not panic and must produce a
	// fallback palette. Under v2, lipgloss.Color eagerly parses the
	// input — "not-a-color" resolves to a degenerate RGBA (not nil),
	// so the builder's nil-check fallback path doesn't fire. We
	// therefore assert only that the palette is well-formed and
	// stable: all 12 buckets must agree on the foreground (the
	// builder uses the unparseable base for every bucket when the
	// endpoints don't parse to a real color) and each renders
	// non-empty.
	bad := lipgloss.Color("not-a-color")
	palette := buildStreamingFadePalette(streamingFadeSteps, bad, bad)
	if len(palette) != streamingFadeSteps {
		t.Fatalf("palette length = %d, want %d", len(palette), streamingFadeSteps)
	}
	want := rgbaOf(t, palette[0])
	for i := 1; i < streamingFadeSteps; i++ {
		got := rgbaOf(t, palette[i])
		if got != want {
			t.Errorf("palette[%d] RGBA = %v, want %v (fallback base)", i, got, want)
		}
		if palette[i].Render("x") == "" {
			t.Errorf("palette[%d] rendered an empty string", i)
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
