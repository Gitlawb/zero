//go:build !unix && !windows

package fscopy

import (
	"fmt"
	"os"
)

// openRegularRead opens a file for reading. On platforms without O_NOFOLLOW
// (!unix && !windows: Plan 9, WASM) we cannot atomically refuse a symlink at
// open time. We reject a final-component symlink with Lstat before opening, and
// the caller (CopyFile, HashTree) additionally fstat's the opened descriptor
// and refuses anything that is not a regular file, so a link swapped into the
// path after this check still cannot be silently followed.
func openRegularRead(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrInvalid}
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	// Post-open re-check: if a symlink was swapped in between our Lstat and
	// os.Open, the fd may now reference the link's target. Close and reject.
	if info2, err := os.Lstat(path); err == nil && info2.Mode()&os.ModeSymlink != 0 {
		_ = f.Close()
		return nil, &os.PathError{Op: "open", Path: path, Err: fmt.Errorf("symlink detected after open")}
	}
	return f, nil
}

// openRegularWrite creates or truncates a file for writing. On platforms
// without O_NOFOLLOW (!unix && !windows) we cannot atomically refuse a symlink
// at open time. We perform a two-phase Lstat check (before and after open) to
// narrow the TOCTOU window: if an attacker swaps a symlink into the path
// between the first check and the open, the second check catches it. The
// race window between os.Open and the post-open Lstat still exists but is much
// smaller than a single-check approach. On Windows and Unix, the sibling files
// open_windows.go and open_unix.go use true no-follow opens that eliminate the
// race entirely.
func openRegularWrite(path string, perm uint32) (*os.File, error) {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrInvalid}
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(perm))
	if err != nil {
		return nil, err
	}
	// Post-open re-check: if a symlink was swapped in between our Lstat and
	// os.OpenFile, we may have opened (and truncated) the wrong file. Close
	// and report rather than write data into the symlink target.
	if info2, err := os.Lstat(path); err == nil && info2.Mode()&os.ModeSymlink != 0 {
		_ = f.Close()
		return nil, &os.PathError{Op: "open", Path: path, Err: fmt.Errorf("symlink detected after open")}
	}
	return f, nil
}
