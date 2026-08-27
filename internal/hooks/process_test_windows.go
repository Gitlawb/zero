//go:build windows

package hooks

import (
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

const processStillActive = 259

func awaitHookProcessExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for hookProcessIsActive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("grandchild process %d is still alive after hook cancellation", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func hookProcessIsActive(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return false
	}
	return exitCode == processStillActive
}
