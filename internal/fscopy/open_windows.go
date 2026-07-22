//go:build windows

package fscopy

import (
	"fmt"
	"os"
	"syscall"
)

// fileWriteData is the Windows FILE_WRITE_DATA access right (0x0002). It is
// the minimum access needed for SetEndOfFile and avoids the access-denied error
// that GENERIC_WRITE (which includes FILE_WRITE_ATTRIBUTES) can trigger on
// reparse points opened with FILE_FLAG_OPEN_REPARSE_POINT.
const fileWriteData uint32 = 0x0002

// fileWriteAttributes is the Windows FILE_WRITE_ATTRIBUTES access right
// (0x0100). It is required for os.File.Chmod on Windows, which calls
// SetFileInformationByHandle(FileBasicInfo) on the opened handle. Combined with
// fileWriteData for the existing-regular-file path (step 2) so the handle
// supports both SetEndOfFile and the subsequent Chmod in CopyFile.
const fileWriteAttributes uint32 = 0x0100

// openRegularRead opens a regular file for reading without following a final
// symlink. FILE_FLAG_OPEN_REPARSE_POINT pins a final-component reparse point as
// the object opened by CreateFile; the post-open attribute check then rejects
// it before callers can read through the symlink target.
func openRegularRead(path string) (*os.File, error) {
	f, err := openWindows(path, syscall.GENERIC_READ,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		syscall.OPEN_EXISTING)
	if err != nil {
		return nil, err
	}
	if err := verifyRegularWindowsHandle(syscall.Handle(f.Fd()), path); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

// openRegularWrite creates or truncates a regular file for writing without
// following a final symlink.
//
// The implementation uses a two-step strategy to avoid the Windows limitation
// where CREATE_ALWAYS combined with FILE_FLAG_OPEN_REPARSE_POINT fails when
// the target is an existing reparse point:
//
//  1. CREATE_NEW + FILE_FLAG_OPEN_REPARSE_POINT: atomically creates a fresh
//     file and rejects any existing entry (including reparse points). This is
//     the common path for new files.
//
//  2. OPEN_EXISTING + FILE_FLAG_OPEN_REPARSE_POINT: when the file already
//     exists, opens it without following symlinks, then verifies the handle is
//     not a reparse point before truncating. Using FILE_WRITE_DATA (instead of
//     GENERIC_WRITE) avoids the access-denied error that GENERIC_WRITE triggers
//     on reparse points.
//
// Between step 1 failing and step 2 running, an attacker could replace a
// regular file with a symlink, but step 2 opens with FILE_FLAG_OPEN_REPARSE_POINT,
// which refuses to follow the link, and the post-open reparse-point check
// catches it.
func openRegularWrite(path string, perm uint32) (*os.File, error) {
	pathp, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	attrs := uint32(syscall.FILE_ATTRIBUTE_NORMAL | syscall.FILE_FLAG_BACKUP_SEMANTICS | syscall.FILE_FLAG_OPEN_REPARSE_POINT)

	// Step 1: atomic create-or-fail. If no file (or reparse point) exists at
	// path, this creates it and returns a handle. If anything already exists
	// (including a reparse point), it fails with ERROR_FILE_EXISTS.
	h, err := syscall.CreateFile(pathp, syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.CREATE_NEW, attrs, 0)
	if err == nil {
		return os.NewFile(uintptr(h), path), nil
	}
	if err != syscall.ERROR_FILE_EXISTS {
		return nil, &os.PathError{Op: "create", Path: path, Err: err}
	}

	// Step 2: the file already exists. Open it without following symlinks,
	// verify it is not a reparse point or directory, then truncate.
	//
	// Use FILE_WRITE_DATA | FILE_WRITE_ATTRIBUTES: FILE_WRITE_DATA is sufficient
	// for SetEndOfFile; FILE_WRITE_ATTRIBUTES is needed by os.File.Chmod (used
	// in CopyFile) which calls SetFileInformationByHandle on the handle. Using
	// these specific rights instead of GENERIC_WRITE avoids the access-denied
	// that GENERIC_WRITE triggers on reparse points; reparse points and
	// directories are rejected explicitly in the post-open verification below.
	h, err = syscall.CreateFile(pathp, fileWriteData|fileWriteAttributes,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, attrs, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}

	// Post-open verification: confirm the handle is a regular file — not a
	// reparse point (symlink) and not a directory. This catches a symlink
	// swapped in between the caller's Lstat and this open (the open with
	// FILE_FLAG_OPEN_REPARSE_POINT opened the link itself, not its target),
	// and also catches a directory left at the destination path. SetEndOfFile
	// on a directory handle fails with ERROR_INVALID_PARAMETER; rejecting it
	// here gives a clear "not a regular file" error instead.
	if err := verifyRegularWindowsHandle(h, path); err != nil {
		syscall.CloseHandle(h)
		return nil, err
	}

	// Truncate the existing regular file to zero bytes.
	if err := syscall.SetEndOfFile(h); err != nil {
		syscall.CloseHandle(h)
		return nil, &os.PathError{Op: "truncate", Path: path, Err: err}
	}

	return os.NewFile(uintptr(h), path), nil
}

func verifyRegularWindowsHandle(h syscall.Handle, path string) error {
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(h, &info); err != nil {
		return &os.PathError{Op: "stat", Path: path, Err: err}
	}
	if info.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 ||
		info.FileAttributes&syscall.FILE_ATTRIBUTE_DIRECTORY != 0 {
		return &os.PathError{Op: "open", Path: path, Err: fmt.Errorf("not a regular file")}
	}
	return nil
}

func openWindows(path string, access, share, disposition uint32) (*os.File, error) {
	pathp, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	attrs := uint32(syscall.FILE_ATTRIBUTE_NORMAL |
		syscall.FILE_FLAG_BACKUP_SEMANTICS |
		syscall.FILE_FLAG_OPEN_REPARSE_POINT)
	h, err := syscall.CreateFile(pathp, access, share, nil, disposition, attrs, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}
