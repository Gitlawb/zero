package tui

import (
	"context"
	"testing"
)

// The caller decides what a session should know before its first prompt; the
// model shows each notice as a system row, in order, and skips blank entries so
// a caller can pass one unconditionally.
func TestStartupNoticesOpenTheSession(t *testing.T) {
	m := newModel(context.Background(), Options{StartupNotices: []string{"first notice", "  ", "", "second notice"}})

	var shown []string
	for _, row := range m.transcript {
		if row.kind == rowSystem {
			shown = append(shown, row.text)
		}
	}
	if len(shown) != 2 || shown[0] != "first notice" || shown[1] != "second notice" {
		t.Fatalf("system rows = %q, want the two non-blank notices in order", shown)
	}
}
