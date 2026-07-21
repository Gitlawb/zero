//go:build !linux

package sandbox

import "errors"

// ErrSeccompUnsupported is returned by ApplyUnixSocketBlock on non-Linux
// platforms, where seccomp BPF is unavailable.
var ErrSeccompUnsupported = errors.New("seccomp Unix-socket blocking is only supported on Linux")

// ApplyUnixSocketBlock is unsupported on non-Linux platforms and always
// returns ErrSeccompUnsupported.
func ApplyUnixSocketBlock() error { return ErrSeccompUnsupported }

// ApplyLinuxNetworkDeny is unsupported on non-Linux platforms and always
// returns ErrSeccompUnsupported.
func ApplyLinuxNetworkDeny() error { return ErrSeccompUnsupported }

// ApplyLinuxIsolatedNetworkGuard is unsupported on non-Linux platforms and
// always returns ErrSeccompUnsupported.
func ApplyLinuxIsolatedNetworkGuard() error { return ErrSeccompUnsupported }
