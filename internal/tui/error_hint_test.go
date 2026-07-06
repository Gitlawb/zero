package tui

import (
	"strings"
	"testing"
)

// A hinted error row renders the raw error and the faint one-line next step
// below it; an unhinted row renders just the error.
func TestRenderErrorRowShowsHint(t *testing.T) {
	withHint := renderErrorRow(transcriptRow{
		kind: rowError,
		text: "auth error: your API key is missing or invalid",
		hint: "API key rejected — run /provider to re-check your credentials",
	}, 80)
	if !strings.Contains(withHint, "auth error:") {
		t.Fatalf("expected raw error text in output, got:\n%s", withHint)
	}
	if !strings.Contains(withHint, "/provider") {
		t.Fatalf("expected hint referencing /provider, got:\n%s", withHint)
	}

	noHint := renderErrorRow(transcriptRow{kind: rowError, text: "provider error: mystery"}, 80)
	if strings.Contains(noHint, "→") {
		t.Fatalf("unhinted error must not render a hint arrow, got:\n%s", noHint)
	}
}
