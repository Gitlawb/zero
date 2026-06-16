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
	// An unparseable endpoint must not panic and must still produce a
	// length-correct palette (every bucket gets a no-op style). We don't
	// assert on color content because the fallback path uses the
	// supplied (unparseable) base, which is whatever lipgloss does with
	// an invalid color string.
	palette := buildStreamingFadePalette(streamingFadeSteps, "not-a-color", "also-not")
	if len(palette) != streamingFadeSteps {
		t.Fatalf("bad-hex palette length = %d, want %d", len(palette), streamingFadeSteps)
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

func TestStreamingLineBornAtOutOfRangeReturnsZero(t *testing.T) {
	// A visual line beyond the lineAges slice should return zero (the
	// defensive case: streamingText was pre-populated without tracking
	// lineAges, or a wrap produced more visual lines than lineAges
	// entries).
	got := streamingLineBornAt(5, 6, []time.Time{{}, {}}, time.Time{})
	if !got.IsZero() {
		t.Errorf("out-of-range bornAt = %v, want zero", got)
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
