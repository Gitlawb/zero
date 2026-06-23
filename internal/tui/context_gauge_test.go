package tui

import (
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/usage"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// The context-fill gauge is empty before any request, then shows
// used/window · pct from the last turn's input tokens against the model window.
func TestContextWindowSegment(t *testing.T) {
	m := newModel(t.Context(), Options{ModelName: "claude-sonnet-4.5"})
	if got := m.contextWindowSegment(); got != "" {
		t.Fatalf("expected empty gauge before any request, got %q", got)
	}
	// 161k latest-step tokens against the 200k window = 81%.
	if _, err := m.usageTracker.Record(usage.RecordInput{
		ModelID: "claude-sonnet-4.5",
		Usage:   zeroruntime.Usage{InputTokens: 160_000, OutputTokens: 1000},
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	got := plainRender(t, m.contextWindowSegment())
	if !strings.Contains(got, "161K/200K") || !strings.Contains(got, "81%") {
		t.Fatalf("gauge = %q, want 161K/200K · 81%%", got)
	}
}
