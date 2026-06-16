package tui

import (
	"context"
	"strings"
	"testing"
)

func TestResumeTextHandlesNilSessionStore(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.sessionStore = nil // force the defensive path

	got := m.resumeText() // must not panic
	if !strings.Contains(got, "unavailable") {
		t.Fatalf("expected a safe 'store unavailable' message, got %q", got)
	}
}
