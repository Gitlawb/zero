package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestMouseEventFilterThrottlesWheelAndMotion(t *testing.T) {
	base := time.Unix(0, 0)
	times := []time.Time{
		base,
		base.Add(10 * time.Millisecond),
		base.Add(14 * time.Millisecond),
		base.Add(15 * time.Millisecond),
	}
	index := 0
	filter := newMouseEventFilter(func() time.Time {
		current := times[index]
		index++
		return current
	}, 15*time.Millisecond)

	if got := filter(nil, testMouseWheel(tea.MouseWheelDown, 0, 0)); got == nil {
		t.Fatal("first wheel event should pass through")
	}
	if got := filter(nil, tea.MouseMotionMsg(tea.Mouse{X: 1, Y: 1})); got != nil {
		t.Fatal("motion event inside throttle window should be dropped")
	}
	if got := filter(nil, testMouseWheel(tea.MouseWheelUp, 0, 0)); got != nil {
		t.Fatal("wheel event inside throttle window should be dropped")
	}
	if got := filter(nil, tea.MouseMotionMsg(tea.Mouse{X: 2, Y: 2})); got == nil {
		t.Fatal("mouse event at throttle boundary should pass through")
	}
}

func TestMouseEventFilterDoesNotThrottleKeyboard(t *testing.T) {
	called := false
	filter := newMouseEventFilter(func() time.Time {
		called = true
		return time.Unix(0, 0)
	}, 15*time.Millisecond)

	msg := testKey('x')
	if got := filter(nil, msg); got != msg {
		t.Fatalf("keyboard event = %#v, want original message", got)
	}
	if called {
		t.Fatal("keyboard events should not touch the mouse throttle clock")
	}
}
