package verify

import "testing"

func TestEventsIncludeSummary(t *testing.T) {
	events := Events()
	if len(events) == 0 || events[len(events)-1].Type != "summary" {
		t.Fatalf("events = %#v, want trailing summary", events)
	}
}
