//go:build windows

package sandbox

import (
	"os"

	"golang.org/x/sys/windows"
)

// createRuntimeDirIdentified creates path and returns the identity of the
// directory it created, taken from the creation handle.
//
// os.Mkdir followed by runtimeDirIdentity(path) is two resolutions of one name:
// Mkdir creates A, and the reopen can land on a B substituted in between, so the
// rollback ledger records B's identity for a directory this run never made.
// Compensation then proves it holds B and deletes it, while A keeps this run's
// ACL and stamp under a name nothing is tracking.
//
// FILE_CREATE fails if anything already exists at the name, which is the same
// os.IsExist signal the caller already handles, and the returned handle IS the
// object created, so its identity cannot describe anything else.
func createRuntimeDirIdentified(path string) (string, bool, error) {
	utf16Path, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", false, err
	}
	handle, err := windows.CreateFile(
		utf16Path,
		windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.CREATE_NEW,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_FLAG_POSIX_SEMANTICS,
		0,
	)
	if err != nil {
		// CreateFile cannot make a directory; fall back to Mkdir plus an
		// immediate no-follow open, which is still two steps but keeps the
		// window to the creation itself rather than to a later reopen.
		if mkErr := os.Mkdir(path, 0o700); mkErr != nil {
			return "", false, mkErr
		}
		identity, ok := runtimeDirIdentity(path)
		return identity, ok, nil
	}
	defer windows.CloseHandle(handle)
	identity, idErr := handleRuntimeIdentity(handle)
	if idErr != nil {
		return "", false, nil
	}
	return identity, true, nil
}
