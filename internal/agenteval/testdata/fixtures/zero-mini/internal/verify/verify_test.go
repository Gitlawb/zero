package verify

import "testing"

func TestEventsIncludeSummary(t *testing.T) {
	events := Events()
	if len(events) == 0 {
		t.Fatalf("events = %#v, want non-empty", events)
	}
	last := events[len(events)-1]
	if last.Type != "summary" || last.Name != "verify" {
		t.Fatalf("last event = %#v, want Type %q and Name %q", last, "summary", "verify")
	}
}
