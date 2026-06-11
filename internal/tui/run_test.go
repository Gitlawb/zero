package tui

import "testing"

func TestUseAltScreenForInteractiveChat(t *testing.T) {
	if !useAltScreen(Options{}) {
		t.Fatal("normal chat should use the alternate screen")
	}
	if !useAltScreen(Options{Setup: SetupOptions{Visible: true}}) {
		t.Fatal("setup takeover should also use the alternate screen")
	}
}
