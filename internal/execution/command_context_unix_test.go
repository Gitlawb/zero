//go:build !windows

package execution

import (
	"testing"
	"time"
)

func awaitProcessExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for signalTargetRunning(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("descendant process %d is still running after command cancellation", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
