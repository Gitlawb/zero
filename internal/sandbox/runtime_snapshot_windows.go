//go:build windows

package sandbox

import (
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// snapshotRuntimeStampBound reads the runtime root's identity and its existing
// stamp through ONE handle.
//
// The two used to be taken separately: runtimeDirIdentity opened the root, read
// its volume and file ID, and closed the handle, and then os.ReadFile resolved
// the pathname again. A rename and substitution in that interval pairs A's
// identity with B's stamp bytes, and a rollback that correctly proves it holds A
// then writes B's bytes into it, corrupting an attestation that predates this
// run. The lease stops cleanup selecting the root; it does not stop the parent's
// owner renaming it.
//
// Reading the child relative to the identified handle removes the second
// resolution, so both facts describe the same object by construction.
func snapshotRuntimeStampBound(root string) (identity string, identified bool, prior []byte, existed bool) {
	utf16Root, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return "", false, nil, false
	}
	directory, err := windows.CreateFile(
		utf16Root,
		windows.FILE_TRAVERSE|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return "", false, nil, false
	}
	defer windows.CloseHandle(directory)

	identity, idErr := handleRuntimeIdentity(directory)
	if idErr != nil {
		return "", false, nil, false
	}

	stamp, err := openWindowsChildNoFollow(directory, windowsSandboxRuntimeStampName,
		windows.GENERIC_READ|windows.FILE_READ_ATTRIBUTES, windows.FILE_NON_DIRECTORY_FILE)
	if err != nil {
		// Absent, or unreadable and therefore not something to put back. The
		// identity still stands: it came from the handle above, not from this.
		return identity, true, nil, false
	}
	file := os.NewFile(uintptr(stamp), windowsSandboxRuntimeStampName)
	defer file.Close()
	data, readErr := io.ReadAll(file)
	if readErr != nil {
		return identity, true, nil, false
	}
	return identity, true, data, true
}
