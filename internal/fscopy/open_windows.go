//go:build windows

package fscopy

import (
	"os"
	"syscall"
)

// openRegularRead opens a regular file for reading without following a final
// symlink: FILE_FLAG_OPEN_REPARSE_POINT makes CreateFile fail if the path is a
// symlink, so a path that was stat'd as a regular file cannot be swapped for a
// link between the check and the open.
func openRegularRead(path string) (*os.File, error) {
	return openWindows(path, syscall.GENERIC_READ,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		syscall.OPEN_EXISTING)
}

// openRegularWrite creates or truncates a regular file for writing without
// following a final symlink: FILE_FLAG_OPEN_REPARSE_POINT makes CreateFile fail
// if the path is a symlink, so a pre-placed symlink at the destination is
// refused instead of being followed and the copy cannot be redirected elsewhere.
func openRegularWrite(path string, perm uint32) (*os.File, error) {
	return openWindows(path, syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		syscall.CREATE_ALWAYS)
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
