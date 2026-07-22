package sandbox

import "strings"

// Shared basename policies and pure helpers for the Windows descendant-scan
// fail-closed rules. The Win32 walk lives in windows_acl_descendants_windows.go;
// these helpers are compiled on every GOOS so non-Windows tests can pin the
// policy without the Windows APIs.

// windowsDescendantScanSystemLockedNames are basenames that Windows keeps
// exclusive to SYSTEM (or otherwise unreadable even to elevated Administrators
// without taking ownership). They appear under every fixed volume root. Listing
// or DACL-reading them fails on healthy machines; treating that as incomplete
// coverage would make DenyRead setup fail everywhere. They never grant
// BUILTIN\Users / Authenticated Users write in stock configuration.
var windowsDescendantScanSystemLockedNames = map[string]struct{}{
	"system volume information": {},
	"$recycle.bin":              {},
	"recovery":                  {},
}

func windowsDescendantScanNameIsSystemLocked(name string) bool {
	_, ok := windowsDescendantScanSystemLockedNames[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

// windowsPathIsDriveRootPath reports whether path is exactly a drive letter
// root such as "C:\" or "C:" (case-insensitively), with no further path
// segments. Used to scope the windowsDescendantScanNameIsSystemLocked
// exception to the one place those basenames are ever legitimately the real,
// SYSTEM-exclusive Windows directory: directly under an actual volume root.
// A directory sharing one of those basenames anywhere else in the tree (e.g.
// nested under ProgramData or Public, whether by installer accident or
// deliberately) is not the real thing and must not be silently skipped — see
// jatmn's review. Pure string check so non-Windows tests can pin it without
// Win32 or filepath's platform-dependent volume parsing.
func windowsPathIsDriveRootPath(path string) bool {
	trimmed := strings.TrimSuffix(strings.TrimSpace(path), `\`)
	if len(trimmed) != 2 || trimmed[1] != ':' {
		return false
	}
	c := trimmed[0]
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

// windowsMountPathIsOnlySystemDrive reports whether a volume mount path is the
// system drive root (e.g. `C:\` or `C:`) rather than another letter or a
// folder mount such as `C:\mnt\data`. Used by the volume gate so a second
// fixed volume mounted only as a folder is rejected the same as one mounted
// on a drive letter.
func windowsMountPathIsOnlySystemDrive(mountPath, systemDrive string) bool {
	trimmed := strings.TrimSuffix(mountPath, `\`)
	return strings.EqualFold(strings.ToUpper(trimmed), strings.ToUpper(systemDrive))
}

// windowsMountPathsAreOnlySystemDrive reports whether mountPaths (a fixed
// volume's DOS/folder mount points from GetVolumePathNamesForVolumeName) name
// the system drive root and nothing else. A fixed volume with NO mount paths
// at all is still directly reachable through its raw "\\?\Volume{GUID}\"
// path even though it has no conventional mount point, so an empty list
// fails closed (false) instead of being read as "unreachable." Pure string
// logic so non-Windows tests can pin the fail-closed cases without Win32.
func windowsMountPathsAreOnlySystemDrive(mountPaths []string, systemDrive string) bool {
	if len(mountPaths) == 0 {
		return false
	}
	for _, mountPath := range mountPaths {
		if !windowsMountPathIsOnlySystemDrive(mountPath, systemDrive) {
			return false
		}
	}
	return true
}
