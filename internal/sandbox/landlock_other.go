//go:build !linux

package sandbox

import "errors"

var ErrLandlockUnsupported = errors.New("Landlock is only supported on Linux")

func ApplyLandlockFilesystemProfile(profile PermissionProfile, cwd string) error {
	return ErrLandlockUnsupported
}
