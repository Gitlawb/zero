//go:build windows

package securefile

import (
	"errors"

	"golang.org/x/sys/windows"
)

func isTransientSecretAccessError(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
