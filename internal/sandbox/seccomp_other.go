//go:build !linux

package sandbox

import "errors"

// ErrSeccompUnsupported is returned by ApplyUnixSocketBlock on non-Linux
// platforms, where seccomp BPF is unavailable.
var ErrSeccompUnsupported = errors.New("seccomp Unix-socket blocking is only supported on Linux")

// ApplyUnixSocketBlock is a no-op on non-Linux platforms.
func ApplyUnixSocketBlock() error { return ErrSeccompUnsupported }

// ApplyLinuxNetworkDeny is a no-op on non-Linux platforms.
func ApplyLinuxNetworkDeny() error { return ErrSeccompUnsupported }
