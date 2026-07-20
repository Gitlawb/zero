//go:build !windows

package providerio

import "syscall"

// dialPreSendErrnos are the syscall errnos a refused or unreachable DIAL raises
// on this platform. On POSIX the standard syscall constants are exactly what the
// net package surfaces, so matching them via errors.Is is sufficient.
var dialPreSendErrnos = []error{
	syscall.ECONNREFUSED,
	syscall.ENETUNREACH,
	syscall.EHOSTUNREACH,
}
