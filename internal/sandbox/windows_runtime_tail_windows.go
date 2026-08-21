//go:build windows

package sandbox

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// windowsFileAddFile is FILE_ADD_FILE, the directory right to create a file in
// it. x/sys/windows does not export it.
const windowsFileAddFile = 0x00000002

// A PATHNAME IS NOT AN OBJECT.
//
// refuseReparsedRuntimeAncestors inspects the owned components before and after
// creation, and that is a check-then-use however many times it runs. Everything
// afterwards reopens the tree BY NAME: openWindowsACLTarget passes the whole
// path to CreateFile, and FILE_FLAG_OPEN_REPARSE_POINT governs only the final
// component, so every ancestor is resolved normally. A local user who plants a
// junction at an owned ancestor between the last check and the open gets the
// capability ACL written to an ordinary leaf inside a directory they chose.
// Windows junctions need no privilege to create, so this is not a theoretical
// attacker.
//
// A second Lstat narrows that window; it cannot close it. The only thing that
// closes it is never resolving the path again: open the base once, then descend
// one component at a time RELATIVE TO THE HANDLE ABOVE IT, refusing a reparse
// point at each step, and use the handle that comes out for everything that
// follows. NtCreateFile is what allows a relative open at all; Win32 CreateFile
// has no equivalent.

// openWindowsRuntimeTailDirectory walks the components Zero owns and returns a
// handle to the runtime root itself. The caller closes it.
func openWindowsRuntimeTailDirectory(root string, access uint32) (windows.Handle, error) {
	base, components, ok := windowsSandboxRuntimeOwnedTail(root)
	if !ok {
		// NOT a fallback to opening by name. A path that does not have a runtime
		// root's shape is one this traversal cannot vouch for, and the whole point
		// is to stop trusting a name.
		return 0, runtimeTailNotOwned(root)
	}
	// The base belongs to the user, so it is opened by name and its own reparse
	// points are followed: a redirected LOCALAPPDATA is an ordinary machine
	// configuration, not an attack.
	parent, err := openWindowsDirectoryByName(base)
	if err != nil {
		return 0, fmt.Errorf("open sandbox runtime base %s: %w", base, err)
	}
	for index, name := range components {
		// FILE_READ_ATTRIBUTES on every component, including the intermediates:
		// each open is followed by a GetFileInformationByHandle to decide whether
		// it is a reparse point, and without that right the check itself fails with
		// "Access is denied" and refuses the whole tree.
		wanted := access | windows.FILE_READ_ATTRIBUTES
		if index < len(components)-1 {
			// Intermediate components are only traversed.
			wanted = windows.FILE_TRAVERSE | windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE
		}
		child, err := openWindowsChildNoFollow(parent, name, wanted, windows.FILE_DIRECTORY_FILE)
		_ = windows.CloseHandle(parent)
		if err != nil {
			return 0, err
		}
		parent = child
	}
	return parent, nil
}

func openWindowsDirectoryByName(path string) (windows.Handle, error) {
	utf16Path, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	return windows.CreateFile(
		utf16Path,
		windows.FILE_TRAVERSE|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
}

// openWindowsChildNoFollow opens exactly one component beneath parent.
//
// FILE_OPEN_REPARSE_POINT makes the open land on a link rather than following
// it, so a swapped component is opened as the link it is and then refused,
// instead of silently resolving into somebody else's tree. Since the name is
// relative to a handle, no ancestor is re-resolved and there is no interval for
// a swap to land in.
func openWindowsChildNoFollow(parent windows.Handle, name string, access uint32, options uint32) (windows.Handle, error) {
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, fmt.Errorf("encode sandbox runtime component %s: %w", name, err)
	}
	attributes := windows.OBJECT_ATTRIBUTES{
		RootDirectory: parent,
		ObjectName:    objectName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE,
	}
	attributes.Length = uint32(unsafe.Sizeof(attributes))

	var handle windows.Handle
	var iosb windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(
		&handle,
		access|windows.SYNCHRONIZE,
		&attributes,
		&iosb,
		nil,
		0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN,
		options|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_REPARSE_POINT,
		0,
		0,
	)
	if err != nil {
		return 0, fmt.Errorf("open sandbox runtime component %s: %w", name, err)
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		_ = windows.CloseHandle(handle)
		return 0, fmt.Errorf("inspect sandbox runtime component %s: %w", name, err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = windows.CloseHandle(handle)
		return 0, fmt.Errorf("refusing to use the sandbox runtime through a link at %s: a reparse point here redirects the directory the sandbox is granted write access to", name)
	}
	return handle, nil
}

// writeWindowsRuntimeStampThroughHandle writes the setup stamp INTO the object
// the traversal reached, not into whatever the pathname resolves to now.
//
// The old writer used MkdirAll and a pathname write, which left a second
// unbound interval: a tree replaced after the ACL apply could be recreated and
// stamped without ever carrying the capability grant, and marker validation
// still passed because it only reads the stamp's contents. The restricted
// process then got a marker-valid runtime path with no grant on it.
func writeWindowsRuntimeStampThroughHandle(root string, planHash string) error {
	directory, err := openWindowsRuntimeTailDirectory(root, windows.FILE_TRAVERSE|windowsFileAddFile|windows.SYNCHRONIZE)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(directory)

	objectName, err := windows.NewNTUnicodeString(windowsSandboxRuntimeStampName)
	if err != nil {
		return fmt.Errorf("encode sandbox runtime setup stamp name: %w", err)
	}
	attributes := windows.OBJECT_ATTRIBUTES{
		RootDirectory: directory,
		ObjectName:    objectName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE,
	}
	attributes.Length = uint32(unsafe.Sizeof(attributes))

	var handle windows.Handle
	var iosb windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(
		&handle,
		windows.GENERIC_WRITE|windows.SYNCHRONIZE,
		&attributes,
		&iosb,
		nil,
		windows.FILE_ATTRIBUTE_NORMAL,
		windows.FILE_SHARE_READ,
		windows.FILE_OVERWRITE_IF,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_REPARSE_POINT,
		0,
		0,
	)
	if err != nil {
		return fmt.Errorf("write sandbox runtime setup stamp: %w", err)
	}
	file := os.NewFile(uintptr(handle), windowsSandboxRuntimeStampName)
	defer file.Close()
	if _, err := file.WriteString(planHash); err != nil {
		return fmt.Errorf("write sandbox runtime setup stamp: %w", err)
	}
	return nil
}
