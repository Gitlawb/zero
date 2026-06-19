package agent

import (
	"strings"
	"testing"
)

func TestResponseStyleContext(t *testing.T) {
	for _, blank := range []string{"", "balanced", "BALANCED", "garbage", "  "} {
		if got := responseStyleContext(Options{ResponseStyle: blank}); got != "" {
			t.Errorf("ResponseStyle %q should add nothing, got %q", blank, got)
		}
	}
	for _, style := range []string{"concise", "explanatory", "review", "Concise", "REVIEW"} {
		got := responseStyleContext(Options{ResponseStyle: style})
		if got == "" {
			t.Fatalf("style %q produced no directive", style)
		}
		if !strings.Contains(strings.ToLower(got), strings.ToLower(style)) {
			t.Errorf("style %q directive should name the style, got %q", style, got)
		}
	}
	// It lands in the assembled prompt for a real style, and not for balanced.
	if !strings.Contains(buildSystemPrompt(Options{ResponseStyle: "concise"}), "Response style: concise") {
		t.Error("buildSystemPrompt should inject the concise directive")
	}
	if strings.Contains(buildSystemPrompt(Options{ResponseStyle: "balanced"}), "Response style:") {
		t.Error("balanced must not inject a style section (prompt stays byte-identical)")
	}
}
