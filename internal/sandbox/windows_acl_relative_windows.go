//go:build windows

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Handle-relative directory operations.
//
// WHY THESE EXIST. Every pathname-based call re-resolves the whole path inside
// the kernel at the moment it runs. So verifying a component and then creating
// through it are two separate resolutions of the same string, and a workspace
// owner can swap an ancestor for a junction in the gap between them: setup
// verifies, the attacker swaps, setup creates, and the object lands outside the
// approved tree. Checking again afterwards is too late, because the thing has
// already been created somewhere it should not be.
//
// A HANDLE pins the object rather than the name. Once a directory is open, that
// handle keeps referring to the same directory however the path is later
// rearranged, so creating a child relative to it cannot be redirected. os.Mkdir,
// os.OpenFile and os.RemoveAll are pathname-based by construction with no
// relative form on Windows, which is why this drops to NtCreateFile with
// OBJECT_ATTRIBUTES.RootDirectory.

// IO_STATUS_BLOCK.Information values for a create/open, named because "2 means
// it was created" is not something a reader should have to look up.
const (
	windowsFileOpened  uintptr = 1
	windowsFileCreated uintptr = 2
)

// windowsACLDirectoryShare is the share mode every open here uses. A sandbox
// tree is live, so refusing to share would fail on any directory something else
// happens to have open: a denial of service on ourselves rather than a security
// property.
const windowsACLDirectoryShare = windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE

// openWindowsACLDirectoryNoFollow opens an existing directory by pathname,
// refusing to traverse or land on a reparse point.
//
// This is the ANCHOR for a handle-relative walk: the one pathname resolution
// that has to happen, with everything below it relative to the handle it
// returns. FILE_FLAG_OPEN_REPARSE_POINT stops the final component being
// followed, and verifyWindowsACLTargetNotRedirected then confirms no ancestor
// redirected either, because GetFinalPathNameByHandle answers for the whole
// resolved path.
func openWindowsACLDirectoryNoFollow(path string) (windows.Handle, error) {
	utf16Path, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, fmt.Errorf("encode windows ACL directory %s: %w", path, err)
	}
	handle, err := windows.CreateFile(
		utf16Path,
		windows.FILE_LIST_DIRECTORY|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE,
		windowsACLDirectoryShare,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		// Errno.Is maps the not-found codes to os.ErrNotExist, so a caller
		// walking up to find the deepest existing ancestor keeps working.
		return 0, fmt.Errorf("open windows ACL directory %s: %w", path, err)
	}
	if err := verifyWindowsACLHandleIsCleanDirectory(handle, path); err != nil {
		_ = windows.CloseHandle(handle)
		return 0, err
	}
	return handle, nil
}

// createWindowsACLChildDirectory creates one directory directly beneath parent,
// or opens it when it already exists, and reports which happened.
//
// name must be a single component. The kernel resolves it relative to the parent
// HANDLE, so nothing above it is consulted and nothing above it can be swapped
// underneath us. FILE_OPEN_REPARSE_POINT means an existing child that is a
// junction is opened AS the junction rather than followed, so the caller's
// verification can reject it.
//
// created is true only when this call made the directory, which the rollback
// needs: removing one that already existed would delete a user's data over a
// failure that had nothing to do with it.
func createWindowsACLChildDirectory(parent windows.Handle, name string) (handle windows.Handle, created bool, err error) {
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, false, fmt.Errorf("encode windows ACL directory component %s: %w", name, err)
	}
	attributes := windows.OBJECT_ATTRIBUTES{
		RootDirectory: parent,
		ObjectName:    objectName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE,
	}
	attributes.Length = uint32(unsafe.Sizeof(attributes))

	var status windows.IO_STATUS_BLOCK
	if err := windows.NtCreateFile(
		&handle,
		windows.FILE_LIST_DIRECTORY|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE|windows.DELETE,
		&attributes,
		&status,
		nil,
		windows.FILE_ATTRIBUTE_DIRECTORY,
		windowsACLDirectoryShare,
		windows.FILE_OPEN_IF,
		windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT,
		0,
		0,
	); err != nil {
		return 0, false, fmt.Errorf("create windows ACL directory component %s: %w", name, err)
	}
	return handle, status.Information == windowsFileCreated, nil
}

// deleteWindowsACLChildDirectory removes one directory directly beneath parent.
//
// The counterpart to the create above, and the reason rollback cannot use
// os.RemoveAll: that takes a pathname, so an ancestor swapped to a junction
// AFTER the object was created sends the recursive delete somewhere else and
// takes unrelated trees with it. Resolving relative to the parent handle makes
// that impossible, and FILE_DIRECTORY_FILE refuses anything that is not a
// directory rather than deleting it.
//
// A missing child is not an error: rollback runs on failure paths where the
// object may never have been created.
func deleteWindowsACLChildDirectory(parent windows.Handle, name string) error {
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return fmt.Errorf("encode windows ACL directory component %s: %w", name, err)
	}
	attributes := windows.OBJECT_ATTRIBUTES{
		RootDirectory: parent,
		ObjectName:    objectName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE,
	}
	attributes.Length = uint32(unsafe.Sizeof(attributes))

	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	if err := windows.NtCreateFile(
		&handle,
		windows.DELETE|windows.SYNCHRONIZE,
		&attributes,
		&status,
		nil,
		windows.FILE_ATTRIBUTE_DIRECTORY,
		windowsACLDirectoryShare,
		windows.FILE_OPEN,
		windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_DELETE_ON_CLOSE,
		0,
		0,
	); err != nil {
		if isWindowsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open windows ACL directory component %s for delete: %w", name, err)
	}
	// FILE_DELETE_ON_CLOSE performs the removal; closing is what commits it.
	if err := windows.CloseHandle(handle); err != nil {
		return fmt.Errorf("delete windows ACL directory component %s: %w", name, err)
	}
	return nil
}

// verifyWindowsACLHandleIsCleanDirectory rejects a handle that landed on a
// reparse point, or on an object other than the path asked for.
func verifyWindowsACLHandleIsCleanDirectory(handle windows.Handle, path string) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return fmt.Errorf("inspect windows ACL directory %s: %w", path, err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("refusing to materialize under reparse-point path component %s: possible path swap during elevated setup", path)
	}
	return verifyWindowsACLTargetNotRedirected(handle, path)
}

// isWindowsNotExist reports a missing-object error from either side of the API.
// NtCreateFile returns NTSTATUS values, which do not map to os.ErrNotExist the
// way the Win32 error codes do.
func isWindowsNotExist(err error) bool {
	if err == nil {
		return false
	}
	if os.IsNotExist(err) {
		return true
	}
	var status windows.NTStatus
	if errors.As(err, &status) {
		return status == windows.STATUS_OBJECT_NAME_NOT_FOUND || status == windows.STATUS_OBJECT_PATH_NOT_FOUND
	}
	return false
}
