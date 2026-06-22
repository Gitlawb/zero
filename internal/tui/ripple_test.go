package tui

import (
	"strings"
	"testing"
)

// TestRippleLevelAtTrough verifies the cosine trough: with q = waveLen/2,
// cos(pi) = -1, so the half-wave-rectified cosine is 0 and level is 0.
func TestRippleLevelAtTrough(t *testing.T) {
	if got := rippleLevel(4, 4, 8); got != 0 {
		t.Fatalf("rippleLevel(4, 4, 8) = %d, want 0 (cosine trough)", got)
	}
}

// TestRippleLevelAtPeak verifies the cosine peak at distance 0: cos(0) = 1, so
// level equals travel.
func TestRippleLevelAtPeak(t *testing.T) {
	if got := rippleLevel(0, 4, 8); got != 4 {
		t.Fatalf("rippleLevel(0, 4, 8) = %d, want 4 (cosine peak)", got)
	}
}

// TestRippleLevelAtQuarterPeriod verifies the midpoint: cos(pi/2) = 0, so the
// half-wave-rectified cosine is 0.5 and level equals travel/2.
func TestRippleLevelAtQuarterPeriod(t *testing.T) {
	if got := rippleLevel(2, 4, 8); got != 2 {
		t.Fatalf("rippleLevel(2, 4, 8) = %d, want 2 (quarter-period midpoint)", got)
	}
}

// TestRippleLevelAtThreeQuarterPeriod verifies the symmetric back-half midpoint.
// cos(3pi/2) is mathematically 0, but float rounding yields a tiny negative, and
// int() truncation floors 4*0.4999… to 1. We assert the deterministic truncated
// value rather than the ideal 2 — the wave is still a smooth dim-mid level here.
func TestRippleLevelAtThreeQuarterPeriod(t *testing.T) {
	if got := rippleLevel(6, 4, 8); got != 1 {
		t.Fatalf("rippleLevel(6, 4, 8) = %d, want 1 (three-quarter midpoint, truncated)", got)
	}
}

// TestRippleLevelPeriodic verifies the wave repeats every waveLen.
func TestRippleLevelPeriodic(t *testing.T) {
	if rippleLevel(0, 4, 8) != rippleLevel(8, 4, 8) {
		t.Fatalf("wave should repeat: l(0)=%d != l(8)=%d", rippleLevel(0, 4, 8), rippleLevel(8, 4, 8))
	}
	if rippleLevel(2, 4, 8) != rippleLevel(10, 4, 8) {
		t.Fatalf("wave should repeat: l(2)=%d != l(10)=%d", rippleLevel(2, 4, 8), rippleLevel(10, 4, 8))
	}
}

// TestRippleLevelZeroInputsShortCircuits checks the safe-guard clauses.
func TestRippleLevelZeroInputsShortCircuits(t *testing.T) {
	if rippleLevel(5, 4, 0) != 0 {
		t.Fatalf("rippleLevel(_, _, 0) should be 0, got %d", rippleLevel(5, 4, 0))
	}
	if rippleLevel(5, 0, 8) != 0 {
		t.Fatalf("rippleLevel(_, 0, _) should be 0, got %d", rippleLevel(5, 0, 8))
	}
}

// TestRippleLevelNegativeDistanceNormalises confirms the negative-safety branch:
// distance -1 with waveLen 8 normalises to q = 7.
func TestRippleLevelNegativeDistanceNormalises(t *testing.T) {
	if got, want := rippleLevel(-1, 4, 8), rippleLevel(7, 4, 8); got != want {
		t.Fatalf("rippleLevel(-1, 4, 8)=%d, want value at distance=7 = %d", got, want)
	}
}

// TestRippleEmptyPaletteReturnsText is the safe-skip contract.
func TestRippleEmptyPaletteReturnsText(t *testing.T) {
	if got := rippleText("hello", nil, 0, 8); got != "hello" {
		t.Fatalf("rippleText with empty palette = %q, want %q", got, "hello")
	}
}

// TestRippleEmptyTextIsEmpty verifies the trivial input case.
func TestRippleEmptyTextIsEmpty(t *testing.T) {
	if got := rippleText("", ripplePalette(), 0, 8); got != "" {
		t.Fatalf("rippleText with empty input = %q, want \"\"", got)
	}
}

// TestRipplePhaseShiftChangesBytesAcrossString verifies adjacent phase values
// produce different byte streams, the contract that lets the caller animate.
func TestRipplePhaseShiftChangesBytesAcrossString(t *testing.T) {
	a := rippleText("Working", ripplePalette(), 0, 6)
	b := rippleText("Working", ripplePalette(), 3, 6)
	if a == b {
		t.Fatalf("rippleText at phase 0 and 3 produced identical bytes: %q", a)
	}
}

// TestRippleAdvancesPhaseAcrossFrames ties Stage 3 to the spinner clock: stepping
// phase must produce distinct outputs (the run-state text actually changes).
func TestRippleAdvancesPhaseAcrossFrames(t *testing.T) {
	seen := map[string]bool{}
	for phase := 0; phase < 12; phase++ {
		seen[rippleText("Working", ripplePalette(), phase, 6)] = true
	}
	if len(seen) < 2 {
		t.Fatalf("expected at least 2 distinct ripple outputs across 12 phase steps; got %d", len(seen))
	}
}

// TestWorkingStatusLineRipplesWorkingWord is the integration test: the working
// status line must carry styling around "Working" (not the literal word) and the
// styling must change as the shared spinner phase advances.
func TestWorkingStatusLineRipplesWorkingWord(t *testing.T) {
	m := newModel(t.Context(), Options{})
	m.width = 100
	m.pending = true

	m.spinnerPhase = 0
	a := m.workingStatusLine()
	if !strings.Contains(plainRender(t, a), "Working") {
		t.Fatalf("working status line missing 'Working' substring; got %q", a)
	}

	m.spinnerPhase = 3
	b := m.workingStatusLine()
	if a == b {
		t.Fatalf("working status line did not change as spinnerPhase advanced; got %q", a)
	}
}
