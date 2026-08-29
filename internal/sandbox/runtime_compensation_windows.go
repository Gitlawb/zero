//go:build windows

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// THE HANDLE IS THE OBJECT. A pathname is not.
//
// Compensation used to read the directory identity through a handle, close it,
// and then resolve the pathname again for the mutation. A rename followed by a
// replacement between those two steps makes the comparison true about one
// object while the write or delete lands on another, and this runs elevated, so
// a redirected pathname gives the mutation reach the replacer does not have
// directly.
//
// Opening once and keeping the handle open across BOTH the identity check and
// the mutation removes the interval. The child operations are relative to that
// handle, so no ancestor is re-resolved either.

// runtimeCompensationSwapSeam runs between the identity check and the mutation.
// Nil in production; a test installs one to replace the directory in exactly the
// window this design closes.
var runtimeCompensationSwapSeam func()

// runtimeCompensationStat is the post-deletion existence probe. A var so a test
// can produce the third outcome, an inspection that neither proves absence nor
// presence, which no real filesystem produces on demand.
var runtimeCompensationStat = os.Lstat

type fileDispositionInfo struct {
	DeleteFile byte
}

func handleRuntimeIdentity(handle windows.Handle) (string, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d:%d", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), nil
}

// openVerifiedRuntimeDirectory opens path without following a link at the final
// component and returns it only if it is still the object identity names.
func openVerifiedRuntimeDirectory(path string, identity string, access uint32, what string) (windows.Handle, error) {
	utf16Path, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	handle, err := windows.CreateFile(
		utf16Path,
		access|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return 0, err
	}
	current, err := handleRuntimeIdentity(handle)
	if err != nil {
		_ = windows.CloseHandle(handle)
		return 0, fmt.Errorf("identify the sandbox runtime directory %s for compensation: %w", path, err)
	}
	if current != identity {
		_ = windows.CloseHandle(handle)
		return 0, fmt.Errorf("sandbox runtime root %s is no longer the directory this run %s; "+
			"leaving the replacement in place, and the original still carries this run's changes", path, what)
	}
	return handle, nil
}

// markForDeletion queues the object the handle names for removal. It never
// resolves a pathname, so it can only reach what this process already holds.
func markForDeletion(handle windows.Handle) error {
	info := fileDispositionInfo{DeleteFile: 1}
	return windows.SetFileInformationByHandle(
		handle,
		windows.FileDispositionInfo,
		(*byte)(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
}

// compensateRuntimeStampBound restores or removes the stamp through a handle on
// the directory whose identity still matches.
//
// Both branches DELETE first. The stamp carries a protected DACL that withholds
// write from the root owner, so an in-place overwrite is denied under the very
// token that wrote it; and recreating it through the ordinary writer is what
// puts that DACL back, which a raw write would not.
func compensateRuntimeStampBound(root string, identity string, prior []byte, existed bool) error {
	directory, err := openVerifiedRuntimeDirectory(root, identity,
		windows.FILE_TRAVERSE|windowsFileAddFile|windows.READ_CONTROL|windows.SYNCHRONIZE, "stamped")
	if err != nil {
		return err
	}
	defer windows.CloseHandle(directory)

	if runtimeCompensationSwapSeam != nil {
		runtimeCompensationSwapSeam()
	}

	if err := deleteRuntimeStampChild(directory); err != nil {
		return err
	}
	if !existed {
		return nil
	}
	// Recreated through the ordinary writer so it is protected again, and so the
	// reader ACE is resolved the same way a fresh setup resolves it.
	if err := writeWindowsRuntimeStampToDirectoryHandle(directory, string(prior)); err != nil {
		return fmt.Errorf("restore the previous sandbox runtime setup stamp: %w", err)
	}
	return nil
}

// deleteRuntimeStampChild removes the stamp relative to an already verified
// directory handle. A stamp that is not there is the desired end state; anything
// else is reported rather than swallowed.
func deleteRuntimeStampChild(directory windows.Handle) error {
	stamp, err := openWindowsChildNoFollow(directory, windowsSandboxRuntimeStampName,
		windows.DELETE|windows.FILE_READ_ATTRIBUTES, windows.FILE_NON_DIRECTORY_FILE)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) || errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("remove sandbox runtime setup stamp written by this run: %w", err)
	}
	if err := markForDeletion(stamp); err != nil {
		_ = windows.CloseHandle(stamp)
		return fmt.Errorf("remove sandbox runtime setup stamp written by this run: %w", err)
	}
	// The entry goes when the last handle closes.
	_ = windows.CloseHandle(stamp)
	return nil
}

// removeCreatedRuntimeDirBound removes a directory this run created, through a
// handle on the object identity names.
func removeCreatedRuntimeDirBound(path string, identity string) error {
	handle, err := openVerifiedRuntimeDirectory(path, identity, windows.DELETE, "created")
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if runtimeCompensationSwapSeam != nil {
		runtimeCompensationSwapSeam()
	}
	if err := markForDeletion(handle); err != nil {
		_ = windows.CloseHandle(handle)
		return fmt.Errorf("remove sandbox runtime root %s created by this run: %w", path, err)
	}
	// The entry goes when the last handle closes, so releasing ours is what makes
	// the removal observable.
	_ = windows.CloseHandle(handle)
	// REPORTED SUCCESS IS NOT PROOF. A handle-bound operation accepting the call
	// has been seen not to take effect (PR #751, the promote rename), so the
	// outcome is checked rather than assumed.
	//
	// THREE OUTCOMES, NOT TWO. This used to read any Lstat error as absence, so a
	// sharing violation, an access denial, or a delete-pending entry held open by
	// another process all reported complete compensation for a directory that is
	// still there. A holder that clears the disposition can then make the
	// "removed" object visible again. Only not-found is evidence of removal;
	// everything else is at best unproven and has to be reported as residue.
	_, statErr := runtimeCompensationStat(path)
	switch {
	case errors.Is(statErr, os.ErrNotExist):
		return nil
	case statErr == nil:
		return fmt.Errorf("remove sandbox runtime root %s created by this run: it is still present after the deletion was accepted", path)
	default:
		return fmt.Errorf("remove sandbox runtime root %s created by this run: its removal could not be verified, so it may still be present: %w", path, statErr)
	}
}
