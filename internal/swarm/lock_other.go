//go:build !windows

package swarm

import (
	"errors"
	"os"
)

// isLockContended reports whether an O_EXCL lock-create error means the lock is
// currently held (so the caller should retry/wait) rather than a hard failure.
// On POSIX an existing lock surfaces only as os.ErrExist.
func isLockContended(err error) bool {
	return errors.Is(err, os.ErrExist)
}
