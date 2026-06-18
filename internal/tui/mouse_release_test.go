package tui

import "testing"

func TestMouseReleaseDisablesCapture(t *testing.T) {
	m := model{altScreen: true} // chat surface (setup not visible) wants capture
	if !m.wantsMouseCapture() {
		t.Fatal("chat should want mouse capture by default")
	}
	m.mouseReleased = true
	if m.wantsMouseCapture() {
		t.Fatal("mouseReleased must force mouse capture OFF so native selection/copy works")
	}
}
