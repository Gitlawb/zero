package nav

import "testing"

// TestGreet is a real test so nav-04's "count the test functions" has a
// non-zero, inspection-required answer: an agent that never opens the workspace
// and always emits "count: 0" fails, which is the whole point of graduating nav
// into the correctness tier. It runs green offline (no third-party deps).
func TestGreet(t *testing.T) {
	if got := greet("x"); got != "hello, x" {
		t.Fatalf("greet(x) = %q, want %q", got, "hello, x")
	}
}
