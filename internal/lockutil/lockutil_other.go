//go:build !windows

package lockutil

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

type platformLockState struct{}

func tryLockFile(file *os.File) (platformLockState, bool, error) {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == nil {
		return platformLockState{}, false, nil
	}
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return platformLockState{}, true, nil
	}
	return platformLockState{}, false, err
}

func unlockFile(file *os.File, _ *platformLockState) error {
	if file == nil {
		return nil
	}
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
