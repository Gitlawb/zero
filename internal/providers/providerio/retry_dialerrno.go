//go:build !windows && !plan9

package providerio

import (
	"errors"
	"syscall"
)

// dialPreSendErrnos are the syscall errnos a refused or unreachable DIAL raises
// on this platform. On POSIX the standard syscall constants are exactly what the
// net package surfaces, so matching them via errors.Is is sufficient. Plan 9 is
// excluded because it does not define these BSD-style errno constants (it uses
// string errors), so the constants below would not compile there; Zero targets
// linux/darwin/windows, so no Plan 9 variant is needed.
var dialPreSendErrnos = []error{
	syscall.ECONNREFUSED,
	syscall.ENETUNREACH,
	syscall.EHOSTUNREACH,
}

// isConnResetErrno reports whether err carries a connection-reset errno, the
// post-send exclusion in isPreSendTransportError. It lives here (per platform)
// so retry.go references no syscall constants and stays buildable on Plan 9,
// which defines none of them.
func isConnResetErrno(err error) bool {
	return errors.Is(err, syscall.ECONNRESET)
}
