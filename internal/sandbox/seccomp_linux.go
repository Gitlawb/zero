//go:build linux

package sandbox

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

// ApplyUnixSocketBlock installs a seccomp BPF filter on the CURRENT thread/process
// that denies socket(AF_UNIX, ...) with EPERM, closing the Unix-socket gap that
// the Linux helper's filesystem/network isolation leaves open. It must be called
// in the child, after fork and before exec, so the inner helper stage applies it
// and then execs the real command. It first sets NO_NEW_PRIVS (required to load a
// filter without CAP_SYS_ADMIN), then loads the program.
//
// Not yet verified on real Linux — see seccomp.go. Callers should degrade
// gracefully (run without the filter, with a warning) if this returns an error,
// since the kernel may not support seccomp.
func ApplyUnixSocketBlock() error {
	return applySeccompFilter(unixSocketBlockFilter(), "unix socket block")
}

func ApplyLinuxNetworkDeny() error {
	return applySeccompFilter(networkDenySeccompFilter(), "network deny")
}

func applySeccompFilter(filters []sockFilter, name string) error {
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("seccomp: set no_new_privs: %w", err)
	}
	kernelFilters := make([]unix.SockFilter, len(filters))
	for i, f := range filters {
		kernelFilters[i] = unix.SockFilter{Code: f.Code, Jt: f.Jt, Jf: f.Jf, K: f.K}
	}
	if len(kernelFilters) == 0 {
		return fmt.Errorf("seccomp: empty filter for %s", name)
	}
	prog := unix.SockFprog{
		Len:    uint16(len(kernelFilters)),
		Filter: &kernelFilters[0],
	}
	if err := unix.Prctl(unix.PR_SET_SECCOMP, uintptr(unix.SECCOMP_MODE_FILTER), uintptr(unsafe.Pointer(&prog)), 0, 0); err != nil {
		return fmt.Errorf("seccomp: load %s filter: %w", name, err)
	}
	return nil
}
