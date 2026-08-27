//go:build windows

package execution

import (
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

const processStillActive = 259

func awaitProcessExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for processIsActive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("descendant process %d is still running after command cancellation", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func processIsActive(pid int) bool {
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
