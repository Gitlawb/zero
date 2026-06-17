//go:build !linux

package sandbox

import "errors"

var ErrLandlockUnsupported = errors.New("Landlock is only supported on Linux")

func ApplyLandlockFilesystemProfile(profile PermissionProfile, cwd string, allowNetworkForProxy bool, proxyRouteSpec string) error {
	return ErrLandlockUnsupported
}
