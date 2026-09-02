//go:build !linux

package sandbox

import "errors"

var ErrLandlockUnsupported = errors.New("Landlock is only supported on Linux") //nolint:staticcheck // Preserve the exported sentinel's established text.

func ApplyLandlockFilesystemProfile(profile PermissionProfile, cwd string) error {
	return ErrLandlockUnsupported
}
