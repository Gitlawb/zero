//go:build windows

package sandbox

import (
	"strings"

	"golang.org/x/sys/windows"
)

// windowsSystemDriveIsOnlyFixedVolume reports whether the system drive is
// this machine's only fixed volume.
//
// The compensating shared-directory DenyWrite mitigation covers exactly four
// paths, all on the system drive, while the broadened Users/Authenticated
// Users restricting SIDs participate in every access check on every volume.
// A stock non-system NTFS data volume grants Authenticated Users Modify at
// its root with (OI)(CI)(IO) inheritance, so on a multi-volume host a
// broadened token could write anywhere on such a volume, outside every
// configured write root — and no bounded descendant scan can patch a grant
// inherited volume-wide. The broadening is therefore only sound when there
// is no other fixed volume to protect.
//
// A second fixed volume need not have a drive letter to be reachable: it can
// be mounted at an NTFS folder mount point (e.g. C:\mnt\data), which
// GetLogicalDriveStrings never reports (it only enumerates drive-letter
// roots). This enumerates every volume on the machine via
// FindFirstVolume/FindNextVolume and checks ALL of its mount points —
// drive letters and mounted folders alike — via GetVolumePathNamesForVolumeName,
// so a fixed volume mounted only as a folder is caught the same as one
// mounted on a drive letter.
//
// Fail closed: an enumeration failure, an unresolvable system drive, or any
// additional fixed volume (by any mount path) all report false, keeping the
// narrow restricting-SID set (reads of Users-granted system paths stay
// broken on such hosts, but the write jail holds).
func windowsSystemDriveIsOnlyFixedVolume() bool {
	windowsDir, err := windows.GetSystemWindowsDirectory()
	if err != nil || len(windowsDir) < 2 {
		return false
	}
	systemDrive := strings.ToUpper(windowsDir[:2]) // e.g. "C:"

	volumeNameBuf := make([]uint16, 260)
	handle, err := windows.FindFirstVolume(&volumeNameBuf[0], uint32(len(volumeNameBuf)))
	if err != nil {
		return false
	}
	defer windows.FindVolumeClose(handle)

	for {
		volumeName := windows.UTF16ToString(volumeNameBuf)
		onlySystemDrive, err := windowsVolumeMountsOnlySystemDrive(volumeName, systemDrive)
		if err != nil {
			return false
		}
		if !onlySystemDrive {
			return false
		}
		if err := windows.FindNextVolume(handle, &volumeNameBuf[0], uint32(len(volumeNameBuf))); err != nil {
			if err == windows.ERROR_NO_MORE_FILES {
				break
			}
			return false
		}
	}
	return true
}

// windowsVolumeMountsOnlySystemDrive reports whether volumeName (a
// "\\?\Volume{GUID}\" path from FindFirstVolume/FindNextVolume) is either not
// fixed media, not mounted anywhere, or mounted only at the system drive
// root. Any OTHER mount path — a different drive letter or a folder mount
// point — makes it a reachable extra fixed volume, regardless of which path
// form reaches it.
func windowsVolumeMountsOnlySystemDrive(volumeName, systemDrive string) (bool, error) {
	volumeNamePtr, err := windows.UTF16PtrFromString(volumeName)
	if err != nil {
		return false, err
	}
	if windows.GetDriveType(volumeNamePtr) != windows.DRIVE_FIXED {
		return true, nil
	}

	buf := make([]uint16, 1024)
	var returnLength uint32
	err = windows.GetVolumePathNamesForVolumeName(volumeNamePtr, &buf[0], uint32(len(buf)), &returnLength)
	if err == windows.ERROR_MORE_DATA {
		buf = make([]uint16, returnLength)
		err = windows.GetVolumePathNamesForVolumeName(volumeNamePtr, &buf[0], uint32(len(buf)), &returnLength)
	}
	if err != nil {
		return false, err
	}

	for _, mountPath := range windowsSplitNulList(buf) {
		if !strings.EqualFold(strings.ToUpper(strings.TrimSuffix(mountPath, `\`)), systemDrive) {
			return false, nil
		}
	}
	return true, nil
}

// windowsSplitNulList splits the double-NUL-terminated UTF-16 string list
// returned by GetLogicalDriveStrings into Go strings.
func windowsSplitNulList(buf []uint16) []string {
	var out []string
	start := 0
	for i, c := range buf {
		if c == 0 {
			if i > start {
				out = append(out, windows.UTF16ToString(buf[start:i]))
			}
			start = i + 1
		}
	}
	if start < len(buf) {
		if s := windows.UTF16ToString(buf[start:]); s != "" {
			out = append(out, s)
		}
	}
	return out
}
