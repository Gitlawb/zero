package tui

import (
	"strings"
	"testing"
)

// TestPhaseScrollIndicatorAtTopBlanksUpArrow checks that the up arrow is blanked
// when from == 1 ("nothing to scroll up to").
func TestPhaseScrollIndicatorAtTopBlanksUpArrow(t *testing.T) {
	got := phaseScrollIndicator(1, 5, 20)
	if strings.Contains(got, "↑") {
		t.Fatalf("up arrow should be blank at top; got %q", got)
	}
	if !strings.Contains(got, "↓") {
		t.Fatalf("down arrow should be present when to < total; got %q", got)
	}
	if !strings.Contains(got, "1-5/20") {
		t.Fatalf("indicator should contain '1-5/20'; got %q", got)
	}
}

// TestPhaseScrollIndicatorAtBottomBlanksDownArrow checks the symmetric case at
// the bottom of the transcript: down arrow blanked, up arrow present.
func TestPhaseScrollIndicatorAtBottomBlanksDownArrow(t *testing.T) {
	got := phaseScrollIndicator(16, 20, 20)
	if strings.Contains(got, "↓") {
		t.Fatalf("down arrow should be blank at bottom; got %q", got)
	}
	if !strings.Contains(got, "↑") {
		t.Fatalf("up arrow should be present when from > 1; got %q", got)
	}
	if !strings.Contains(got, "16-20/20") {
		t.Fatalf("indicator should contain '16-20/20'; got %q", got)
	}
}

// TestPhaseScrollIndicatorWholeTranscriptBlanksBothArrows is the degenerate case
// for a single-screen transcript — both arrows blanked.
func TestPhaseScrollIndicatorWholeTranscriptBlanksBothArrows(t *testing.T) {
	got := phaseScrollIndicator(1, 20, 20)
	if strings.Contains(got, "↑") {
		t.Fatalf("up arrow should be blank when from == 1; got %q", got)
	}
	if strings.Contains(got, "↓") {
		t.Fatalf("down arrow should be blank when to == total; got %q", got)
	}
	if !strings.Contains(got, "1-20/20") {
		t.Fatalf("indicator should contain '1-20/20'; got %q", got)
	}
}

// TestPhaseScrollIndicatorMiddleTranscriptShowsBothArrows is the in-flight case
// the indicator is designed for: both arrows visible.
func TestPhaseScrollIndicatorMiddleTranscriptShowsBothArrows(t *testing.T) {
	got := phaseScrollIndicator(8, 12, 20)
	if !strings.Contains(got, "↑ 8-12/20 ↓") {
		t.Fatalf("mid-transcript indicator must be '↑ 8-12/20 ↓'; got %q", got)
	}
}

// TestPhaseScrollIndicatorEmptyTranscriptReturnsEmpty asserts the safe-skip
// contract.
func TestPhaseScrollIndicatorEmptyTranscriptReturnsEmpty(t *testing.T) {
	got := phaseScrollIndicator(0, 0, 0)
	if got != "" {
		t.Fatalf("expected \"\" for empty transcript, got %q", got)
	}
}

// TestPhaseScrollIndicatorClampsFromBelowOne verifies from < 1 is clamped to 1.
func TestPhaseScrollIndicatorClampsFromBelowOne(t *testing.T) {
	got := phaseScrollIndicator(0, 5, 10)
	if strings.Contains(got, "0-5") {
		t.Fatalf("from must be clamped to 1, not 0; got %q", got)
	}
	if !strings.Contains(got, "1-5") {
		t.Fatalf("expected '1-5' after clamp; got %q", got)
	}
}

// TestPhaseScrollIndicatorClampsToAboveTotal verifies to > total collapses to total.
func TestPhaseScrollIndicatorClampsToAboveTotal(t *testing.T) {
	got := phaseScrollIndicator(2, 999, 10)
	if strings.Contains(got, "999") {
		t.Fatalf("to must be clamped to total; got %q", got)
	}
	if !strings.Contains(got, "2-10") {
		t.Fatalf("expected '2-10' after clamp; got %q", got)
	}
}

// TestTranscriptPhaseIndicatorEmptyTranscript verifies the method returns "" for
// an empty transcript so the working line can skip it.
func TestTranscriptPhaseIndicatorEmptyTranscript(t *testing.T) {
	m := newModel(t.Context(), Options{})
	m.transcript = nil // newModel seeds a welcome row; clear it for the empty case
	if got := m.transcriptPhaseIndicator(); got != "" {
		t.Fatalf("empty transcript should yield empty indicator; got %q", got)
	}
}

// TestTranscriptPhaseIndicatorComputesFromTurnBoundary builds a small transcript
// and verifies the indicator reflects total and the latest turn boundary.
func TestTranscriptPhaseIndicatorComputesFromTurnBoundary(t *testing.T) {
	m := newModel(t.Context(), Options{})
	m.transcript = appendRow(nil, rowUser, "go")
	m.transcript = appendRow(m.transcript, rowToolCall, "tool call: read_file")
	m.transcript = appendRow(m.transcript, rowToolResult, "file content")
	m.transcript = appendRow(m.transcript, rowAssistant, "thinking...")
	m.transcript = appendRow(m.transcript, rowToolCall, "tool call: write_file")
	// total = 5, last turn-starting row is the assistant at index 3 (1-indexed 4),
	// to == total == 5, so the indicator shows "↑ 4-5/5" with the down arrow blanked.
	got := m.transcriptPhaseIndicator()
	if !strings.Contains(got, "4-5/5") {
		t.Fatalf("indicator should contain '4-5/5'; got %q", got)
	}
	if strings.Contains(got, "↓") {
		t.Fatalf("down arrow should be blank when to == total; got %q", got)
	}
}

// TestWorkingStatusLineShowsPhaseIndicator is the integration test: the working
// status line must carry the phase indicator when the transcript is non-empty.
func TestWorkingStatusLineShowsPhaseIndicator(t *testing.T) {
	m := newModel(t.Context(), Options{})
	m.width = 100
	m.transcript = appendRow(nil, rowUser, "go")
	m.transcript = appendRow(m.transcript, rowToolCall, "tool call: read_file")
	got := plainRender(t, m.workingStatusLine())
	if !strings.Contains(got, "Working") {
		t.Fatalf("working status line missing 'Working'; got %q", got)
	}
	if !strings.Contains(got, "/2") {
		t.Fatalf("working status line missing phase indicator '/2'; got %q", got)
	}
}
