//go:build windows

package fscopy

import (
	"fmt"
	"os"
	"syscall"
)

// noFollowOpenDir opens the directory named by path without following a final
// symlink or junction. FILE_FLAG_OPEN_REPARSE_POINT pins the final component as
// the reparse point itself; the handle attributes are then checked so CopyTree
// refuses a directory that was swapped for a reparse point between Lstat and
// open.
func noFollowOpenDir(path string) (*os.File, error) {
	f, err := openWindows(path, syscall.GENERIC_READ,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		syscall.OPEN_EXISTING)
	if err != nil {
		return nil, err
	}
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &info); err != nil {
		_ = f.Close()
		return nil, &os.PathError{Op: "stat", Path: path, Err: err}
	}
	if info.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 ||
		info.FileAttributes&syscall.FILE_ATTRIBUTE_DIRECTORY == 0 {
		_ = f.Close()
		return nil, &os.PathError{Op: "open", Path: path, Err: fmt.Errorf("not a directory")}
	}
	return f, nil
}
