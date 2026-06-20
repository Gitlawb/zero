package tui

import (
	"strings"
	"testing"
)

// helpRendersAsCard confirms /help is routed through the styled card path
// (prefixed) rather than the flat grey system note.
func TestHelpRoutesThroughStyledCard(t *testing.T) {
	if _, ok := commandCardTranscriptPayload(helpText()); !ok {
		t.Fatal("helpText() must carry the command-card prefix so it renders as a styled card, not a grey note")
	}
}

// commandOutput screens (/tools, /context, …) also route through the card path.
func TestRenderCommandOutputRoutesThroughStyledCard(t *testing.T) {
	out := renderCommandOutput(commandOutput{Title: "Status", Status: commandStatusInfo})
	if _, ok := commandCardTranscriptPayload(out); !ok {
		t.Fatal("renderCommandOutput must carry the command-card prefix")
	}
}

func TestCommandCardDropsNeutralStatusLine(t *testing.T) {
	m := limeTestModel()
	payload, ok := commandCardTranscriptPayload(helpText())
	if !ok {
		t.Fatal("expected command-card payload")
	}
	got := plainRender(t, renderCommandCardRow(payload, 96))
	if strings.Contains(got, "status: info") {
		t.Fatalf("the neutral 'status: info' line should be dropped, got:\n%s", got)
	}
	// But the real content must survive.
	for _, want := range []string{"Model", "/model", "Session", "/help"} {
		if !strings.Contains(got, want) {
			t.Fatalf("command card missing %q, got:\n%s", want, got)
		}
	}
	_ = m
}

func TestCommandCardKeepsNonOkStatus(t *testing.T) {
	// A warning/blocked status carries signal and must stay visible.
	warn := renderCommandOutput(commandOutput{Title: "Sandbox", Status: commandStatusWarning})
	payload, _ := commandCardTranscriptPayload(warn)
	got := plainRender(t, renderCommandCardRow(payload, 80))
	if !strings.Contains(got, "status: warning") {
		t.Fatalf("a non-ok status should stay visible, got:\n%s", got)
	}
}

func TestStyleCommandCardContentRowTwoTonesCommands(t *testing.T) {
	// A command row keeps the indent, splits name from description on " - ", and
	// applies distinct styles (so the rendered bytes differ from a flat render).
	styled := styleCommandCardContentRow("  /model [id] - switch the model")
	plain := ansiPattern.ReplaceAllString(styled, "")
	if !strings.HasPrefix(plain, "  /model [id]") {
		t.Fatalf("indent + command name should lead the row, got %q", plain)
	}
	if !strings.Contains(plain, "switch the model") {
		t.Fatalf("description should be preserved, got %q", plain)
	}
	// The styled form must actually carry SGR codes (two-tone), i.e. differ from
	// the stripped form.
	if styled == plain {
		t.Fatalf("command row should be styled (carry color), got identical plain/styled: %q", styled)
	}
}

func TestStyleCommandCardContentRowHandlesPlainFields(t *testing.T) {
	// A "key   value" field row (no leading slash, no " - ") stays intact.
	styled := styleCommandCardContentRow("  registered  2")
	plain := ansiPattern.ReplaceAllString(styled, "")
	if plain != "  registered  2" {
		t.Fatalf("field row content should be preserved verbatim, got %q", plain)
	}
}
