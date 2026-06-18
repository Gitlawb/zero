package tui

import (
	"testing"
)

func TestSubchatEnterAndExit(t *testing.T) {
	var s subchatState
	if s.active {
		t.Error("subchat should start inactive")
	}

	// enter with nil store should return an error message
	errMsg := s.enter(nil, "s1", "worker · task", 5)
	if errMsg == "" {
		t.Error("enter with nil store should return error message")
	}

	// exit when not active returns 0
	if offset := s.exit(); offset != 0 {
		t.Errorf("exit when inactive should return 0, got %d", offset)
	}
}

func TestSubchatExitRestoresScrollOffset(t *testing.T) {
	var s subchatState
	// Simulate entering with a saved offset
	s.active = true
	s.childSessionID = "s1"
	s.childSessionTitle = "test"
	s.parentScrollOffset = 42

	offset := s.exit()
	if offset != 42 {
		t.Errorf("exit should return saved offset 42, got %d", offset)
	}
	if s.active {
		t.Error("subchat should be inactive after exit")
	}
	if s.childSessionID != "" {
		t.Error("childSessionID should be cleared after exit")
	}
}

func TestRenderSubchatNavBar(t *testing.T) {
	got := renderSubchatNavBar("worker · fix tests", 80)
	if got == "" {
		t.Fatal("nav bar should not be empty")
	}

	// Empty title should still render
	got2 := renderSubchatNavBar("", 80)
	if got2 == "" {
		t.Fatal("nav bar should not be empty even with no title")
	}
}
