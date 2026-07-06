package tui

import (
	"strings"
	"testing"
)

// "/compact now" is accepted as a manual trigger (not a usage error), while a
// bogus argument still reports usage.
func TestCompactNowAccepted(t *testing.T) {
	m := model{}

	_, nowText, _ := m.handleCompactCommand("now")
	if strings.Contains(nowText, "usage:") {
		t.Fatalf("/compact now must be accepted as a trigger, got usage error: %q", nowText)
	}

	_, statusText, _ := m.handleCompactCommand("status")
	if strings.Contains(statusText, "usage:") {
		t.Fatalf("/compact status must not error, got: %q", statusText)
	}

	_, bogusText, _ := m.handleCompactCommand("frobnicate")
	if !strings.Contains(bogusText, "usage:") {
		t.Fatalf("/compact frobnicate must report usage, got: %q", bogusText)
	}
}
