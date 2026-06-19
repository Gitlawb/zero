package tui

import (
	"strings"
	"testing"
	"time"
)

func TestFormatWorkingElapsed(t *testing.T) {
	cases := map[time.Duration]string{
		0:                 "0s",
		8 * time.Second:   "8s",
		59 * time.Second:  "59s",
		64 * time.Second:  "1m04s",
		125 * time.Second: "2m05s",
		-3 * time.Second:  "0s",
	}
	for d, want := range cases {
		if got := formatWorkingElapsed(d); got != want {
			t.Errorf("formatWorkingElapsed(%s) = %q, want %q", d, got, want)
		}
	}
}

// The key fix: the live working line (spinner + verb + elapsed) is shown even
// AFTER partial text has streamed, so an upstream stall never looks frozen.
func TestInterimBlockShowsWorkingLineWithStreamedText(t *testing.T) {
	m := newModel(t.Context(), Options{ModelName: "gpt-4.1"})
	m.width = 100
	base := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return base.Add(12 * time.Second) }
	m.turnStartedAt = base
	m.streamingText = "partial answer so far"

	got := plainRender(t, m.interimBlock(96))
	if !strings.Contains(got, "partial answer so far") {
		t.Fatalf("interim block should keep the streamed text:\n%s", got)
	}
	if !strings.Contains(got, "12s") {
		t.Fatalf("interim block should show live elapsed (12s) below the text:\n%s", got)
	}
	if !strings.Contains(got, m.workingVerb.Current()) {
		t.Fatalf("interim block should show the working verb for liveness:\n%s", got)
	}
}

func TestPreviewTail(t *testing.T) {
	cases := []struct {
		in    string
		width int
		want  string
	}{
		{"short", 20, "short"},
		{"exactlyten", 10, "exactlyten"},
		{"abcdefghijklmnop", 6, "…lmnop"}, // tail with leading ellipsis
		{"", 8, ""},
	}
	for _, c := range cases {
		if got := previewTail(c.in, c.width); got != c.want {
			t.Errorf("previewTail(%q,%d) = %q, want %q", c.in, c.width, got, c.want)
		}
	}
}

// The fix: during a think (no answer text yet) the streaming reasoning TAIL is
// shown beneath the working line, so the screen shows live, changing content.
func TestInterimBlockShowsReasoningPreviewWhileThinking(t *testing.T) {
	m := newModel(t.Context(), Options{ModelName: "gpt-4.1"})
	m.width = 100
	base := time.Date(2026, 6, 18, 23, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return base.Add(90 * time.Second) }
	m.turnStartedAt = base
	m.streamingReasoning = "analyzing the layout\nthe patch was corrupt so re-planning the css edits"
	m.streamingText = "" // thinking phase: no answer yet

	got := plainRender(t, m.interimBlock(96))
	if !strings.Contains(got, "re-planning the css edits") {
		t.Fatalf("expected the live reasoning tail in the preview:\n%s", got)
	}
	if !strings.Contains(got, "1m30s") {
		t.Fatalf("expected the working-line elapsed clock:\n%s", got)
	}
}

// When the reasoning block is EXPANDED, the full body already shows — the
// collapsed tail preview must NOT be duplicated.
func TestInterimBlockNoPreviewWhenReasoningExpanded(t *testing.T) {
	m := newModel(t.Context(), Options{ModelName: "gpt-4.1"})
	m.width = 100
	m.now = func() time.Time { return time.Date(2026, 6, 18, 23, 0, 0, 0, time.UTC) }
	m.streamingReasoningExpanded = true
	m.streamingReasoning = "only line of reasoning here"
	m.streamingText = ""
	got := plainRender(t, m.interimBlock(96))
	if strings.Count(got, "only line of reasoning here") != 1 {
		t.Fatalf("reasoning should appear exactly once when expanded (no preview dup):\n%s", got)
	}
}
