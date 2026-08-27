//go:build !windows

package hooks

import (
	"errors"
	"syscall"
	"testing"
	"time"
)

func awaitHookProcessExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := syscall.Kill(pid, syscall.Signal(0))
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if err != nil {
			t.Fatalf("probe grandchild process %d: %v", pid, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("grandchild process %d is still alive after hook cancellation", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
