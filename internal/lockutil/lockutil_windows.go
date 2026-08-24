//go:build windows

package lockutil

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

type platformLockState struct {
	overlapped windows.Overlapped
}

func tryLockFile(file *os.File) (platformLockState, bool, error) {
	state := platformLockState{}
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY)
	err := windows.LockFileEx(windows.Handle(file.Fd()), flags, 0, 1, 0, &state.overlapped)
	if err == nil {
		return state, false, nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		return platformLockState{}, true, nil
	}
	return platformLockState{}, false, err
}

func unlockFile(file *os.File, state *platformLockState) error {
	if file == nil {
		return nil
	}
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &state.overlapped)
}
