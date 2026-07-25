//go:build windows

package update

import (
	"encoding/binary"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// createStagedBinary stages beside targetPath under an unpredictable name. Unlike
// POSIX this needs no private directory: Windows can rename an object through the
// very handle it was created with (see promote), so the staging pathname never has
// to be re-resolved and cannot be substituted.
func createStagedBinary(targetPath string) (*stagedBinary, error) {
	path, err := stagingFilePath(targetPath)
	if err != nil {
		return nil, err
	}
	file, err := createStagingFile(path)
	if err != nil {
		return nil, err
	}
	return &stagedBinary{file: file, path: path}, nil
}

// createStagingFile creates path exclusively and without following any
// reparse point that may already occupy it. CREATE_NEW alone can still
// resolve through an existing reparse point (symlink/junction) when deciding
// whether the target exists — if that reparse point's target is a real file
// writable by this (possibly elevated) process, CreateFile would open and
// truncate it instead. FILE_FLAG_OPEN_REPARSE_POINT makes CreateFile operate
// on the reparse point itself, so CREATE_NEW fails on it exactly like it
// would fail on a pre-existing regular file or hard link.
//
// DELETE access is requested alongside GENERIC_WRITE because promote renames
// this object through the handle, which requires it.
func createStagingFile(path string) (*os.File, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_WRITE|windows.DELETE,
		0,
		nil,
		windows.CREATE_NEW,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", path, err)
	}
	if err := verifyFreshRegularFile(handle, path); err != nil {
		_ = windows.CloseHandle(handle)
		_ = os.Remove(path)
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}

// verifyFreshRegularFile defends in depth against the handle unexpectedly
// referring to a reparse point, directory, or an object with other hard
// links: CREATE_NEW + FILE_FLAG_OPEN_REPARSE_POINT should already guarantee
// a brand-new regular file, but this catches any surprise before any of the
// verified release bytes are written through the handle.
func verifyFreshRegularFile(handle windows.Handle, path string) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return fmt.Errorf("stat new staging file %s: %w", path, err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("staging file %s is unexpectedly a reparse point", path)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		return fmt.Errorf("staging file %s is unexpectedly a directory", path)
	}
	if info.NumberOfLinks > 1 {
		return fmt.Errorf("staging file %s unexpectedly has %d hard links", path, info.NumberOfLinks)
	}
	return nil
}

// promote makes the staged object the installed binary. Windows will not let a
// running executable be overwritten or deleted directly, but NTFS does allow
// renaming it aside — the same trick already used for locked config files in
// internal/cli/mcp_config.go's replaceMCPWritableConfigFile.
//
// The second rename goes through the staging HANDLE
// (SetFileInformationByHandle/FileRenameInfo) instead of the staging pathname.
// A pathname rename would re-resolve the staging entry, so a lower-privileged
// principal that can write in the installation directory could replace that entry
// after the verified bytes were written and have the updater install its file
// instead. Renaming the object the handle already refers to removes that handoff:
// there is no second lookup to win.
func (staged *stagedBinary) promote(targetPath string) error {
	oldPath := targetPath + ".old"
	_ = os.Remove(oldPath) // best-effort cleanup of a leftover from a previous upgrade
	if err := os.Rename(targetPath, oldPath); err != nil {
		return fmt.Errorf("rename running binary aside: %w", err)
	}
	renameErr := renameFileByHandle(staged.file, targetPath)
	if renameErr == nil {
		// SetFileInformationByHandle reporting success is not, on its own, proof
		// that targetPath now holds the promoted object: a substituted staging
		// entry can leave the object this handle refers to in a delete-pending
		// state that some Windows versions accept the rename call against
		// without actually completing it, which would otherwise let promote
		// return nil while targetPath is left missing entirely. Confirm the
		// object is actually reachable there before trusting the rename.
		if verifyErr := verifyPromotedTarget(targetPath); verifyErr != nil {
			renameErr = fmt.Errorf("promoted object unreachable at %s: %w", targetPath, verifyErr)
		}
	}
	if renameErr != nil {
		if restoreErr := restoreOriginalBinary(oldPath, targetPath); restoreErr != nil {
			return fmt.Errorf("install new binary: %w; additionally failed to restore the original binary: %v (original preserved at %s)", renameErr, restoreErr, oldPath)
		}
		return fmt.Errorf("install new binary: %w", renameErr)
	}
	staged.path = targetPath
	staged.promoted = true
	return nil
}

// refreshLiveness is a no-op on Windows: cleanup here keys off ".old"/staging
// filename shape rather than a directory mtime (see replace_windows.go), so
// there is no liveness marker for a slow copy to keep fresh.
func (staged *stagedBinary) refreshLiveness() {}

// verifyPromotedTarget reports whether targetPath is reachable immediately
// after a reported-successful rename. SetFileInformationByHandle returning
// success is not, on its own, proof the object actually ended up there: a
// handle whose directory entry was removed and replaced out from under it
// (the substitution createStagingFile's exclusive, no-share open defends
// against — see its doc comment) can leave some Windows versions accepting
// the rename call without it taking effect, which would otherwise let
// promote report success while targetPath is left missing entirely.
//
// os.Stat, not a second CreateFile, does the check: staged.file is still
// open with no sharing, so a second full open of the same object — even
// read-only, even from this process — would itself fail with a sharing
// violation and be indistinguishable from a genuine promotion failure. A
// plain attribute query bypasses share-mode enforcement instead.
func verifyPromotedTarget(targetPath string) error {
	info, err := os.Stat(targetPath)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory, not the promoted binary", targetPath)
	}
	return nil
}

// fileRenameInfo mirrors FILE_RENAME_INFO. FileName is a variable-length WCHAR
// array that follows the header, so the buffer is sized by hand and the name is
// appended after fileRenameInfoHeaderSize bytes.
type fileRenameInfo struct {
	ReplaceIfExists bool
	RootDirectory   windows.Handle
	FileNameLength  uint32
}

// fileRenameInfoHeaderSize is the offset of FILE_RENAME_INFO's FileName member.
// It is derived rather than hardcoded because the RootDirectory pointer's
// alignment (and therefore the offset) differs between 32- and 64-bit Windows.
var fileRenameInfoHeaderSize = func() uintptr {
	var info fileRenameInfo
	return unsafe.Offsetof(info.FileNameLength) + unsafe.Sizeof(info.FileNameLength)
}()

// renameFileByHandle renames the object file refers to, not the object its
// current pathname resolves to. targetPath must be fully qualified.
//
// It is a package var, like stageBinary, so a test can simulate
// SetFileInformationByHandle reporting success without the rename actually
// taking effect — the exact failure mode verifyPromotedTarget defends
// against — without needing to reproduce whatever Windows-version-specific
// condition triggers it for real.
var renameFileByHandle = func(file *os.File, targetPath string) error {
	name, err := windows.UTF16FromString(targetPath)
	if err != nil {
		return err
	}
	name = name[:len(name)-1] // FileNameLength counts bytes without the terminator
	buffer := make([]byte, int(fileRenameInfoHeaderSize)+len(name)*2)
	info := (*fileRenameInfo)(unsafe.Pointer(&buffer[0]))
	// ReplaceIfExists stays false: promote already renamed the running binary
	// aside, so a target that exists again means something raced the update, and
	// failing is better than clobbering whatever appeared there.
	info.FileNameLength = uint32(len(name) * 2)
	for index, unit := range name {
		binary.LittleEndian.PutUint16(buffer[int(fileRenameInfoHeaderSize)+index*2:], unit)
	}
	if err := windows.SetFileInformationByHandle(
		windows.Handle(file.Fd()),
		windows.FileRenameInfo,
		&buffer[0],
		uint32(len(buffer)),
	); err != nil {
		return fmt.Errorf("rename staged binary onto %s: %w", targetPath, err)
	}
	return nil
}
