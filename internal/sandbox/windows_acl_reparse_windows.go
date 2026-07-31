//go:build windows

package sandbox

import (
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// GetFinalPathNameByHandle flags. x/sys/windows does not export these.
const (
	windowsFileNameNormalized uint32 = 0x0
	windowsVolumeNameDOS      uint32 = 0x0
)

// verifyWindowsACLTargetNotRedirected fails when an opened handle resolved
// somewhere other than the requested path, which means a component along the way
// is a reparse point.
//
// openWindowsACLTarget's FILE_FLAG_OPEN_REPARSE_POINT check covers the FINAL
// component only; CreateFile still resolves ANCESTORS. A user who controls the
// workspace can turn an ancestor — .git, say — into a junction before elevated
// setup runs, and setup would then apply its DACL change to an object outside
// the approved tree while the final-component check still passed. Junctions need
// no privilege to create, unlike symlinks, so this is reachable by exactly the
// unprivileged user the sandbox exists to contain.
//
// GetFinalPathNameByHandle answers where the handle actually landed, covering
// every component in one call rather than walking the path and re-checking each
// component (which would also race between the checks).
//
// The comparison is against the path's own resolved form rather than the raw
// string, because a legitimate target can be spelled with different casing or an
// 8.3 short name and still be the same object. Only a genuine redirect makes the
// two disagree.
func verifyWindowsACLTargetNotRedirected(handle windows.Handle, path string) error {
	buffer := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), windowsFileNameNormalized|windowsVolumeNameDOS)
	if err != nil {
		return fmt.Errorf("resolve windows ACL target %s: %w", path, err)
	}
	if int(n) < len(buffer) {
		buffer = buffer[:n]
	}
	actual := trimWindowsExtendedPathPrefix(windows.UTF16ToString(buffer))
	expected := trimWindowsExtendedPathPrefix(canonicalSandboxWorkspaceRoot(path))
	if !strings.EqualFold(filepath.Clean(actual), filepath.Clean(expected)) {
		return fmt.Errorf("refusing to apply ACL to %s: it resolves to %s, so a parent directory is a reparse point (possible path swap during elevated setup)", path, actual)
	}
	return nil
}

// trimWindowsExtendedPathPrefix strips the \?\ form GetFinalPathNameByHandle
// returns so it can be compared with an ordinary path.
func trimWindowsExtendedPathPrefix(path string) string {
	// Built from filepath.Separator rather than written as literals so the
	// backslashes cannot be miscounted by whatever writes this file.
	sep := string(filepath.Separator)
	devicePrefix := sep + sep + "?" + sep
	uncPrefix := devicePrefix + "UNC" + sep
	if strings.HasPrefix(path, uncPrefix) {
		return sep + sep + strings.TrimPrefix(path, uncPrefix)
	}
	return strings.TrimPrefix(path, devicePrefix)
}
