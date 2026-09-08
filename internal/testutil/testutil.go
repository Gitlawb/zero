// Package testutil provides shared test helpers for Zero.
package testutil

import (
	"testing"
	"time"
)

// WaitFor polls cond until it returns true or the default deadline (5s) is
// reached. The what argument names what is being waited for in the failure
// message. Deadline and interval are generous enough for loaded CI runners.
func WaitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
