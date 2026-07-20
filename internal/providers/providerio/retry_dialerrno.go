//go:build !windows && !plan9

package providerio

import "syscall"

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
