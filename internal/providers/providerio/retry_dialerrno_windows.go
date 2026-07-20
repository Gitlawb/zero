//go:build windows

package providerio

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// dialPreSendErrnos are the errnos a refused or unreachable DIAL raises on
// Windows. A real Windows dial failure carries the raw Winsock error
// (WSAECONNREFUSED = 10061, WSAENETUNREACH = 10051, WSAEHOSTUNREACH = 10065), and
// errors.Is against the POSIX syscall.ECONNREFUSED returns FALSE there because
// Go's Windows syscall.ECONNREFUSED is a distinct value the net package never
// produces. So the WSA codes must be matched explicitly, or a refused dial would
// silently never be retried on Windows. The POSIX constants stay in the list too
// so an error built with them (e.g. a test fixture) still classifies.
var dialPreSendErrnos = []error{
	syscall.ECONNREFUSED,
	syscall.ENETUNREACH,
	syscall.EHOSTUNREACH,
	windows.WSAECONNREFUSED,
	windows.WSAENETUNREACH,
	windows.WSAEHOSTUNREACH,
}
