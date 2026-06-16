package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

const mouseEventThrottleInterval = 15 * time.Millisecond

func mouseEventFilter() func(tea.Model, tea.Msg) tea.Msg {
	return newMouseEventFilter(time.Now, mouseEventThrottleInterval)
}

func newMouseEventFilter(now func() time.Time, minInterval time.Duration) func(tea.Model, tea.Msg) tea.Msg {
	var last time.Time
	return func(_ tea.Model, msg tea.Msg) tea.Msg {
		switch msg.(type) {
		case tea.MouseWheelMsg, tea.MouseMotionMsg:
			current := now()
			if !last.IsZero() && current.Sub(last) < minInterval {
				return nil
			}
			last = current
		}
		return msg
	}
}
