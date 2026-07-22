//go:build unix

package fscopy

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// openRegularRead opens a regular file for reading without following a final
// symlink: O_NOFOLLOW makes the open fail (ELOOP) if the path is a symlink,
// so a path that was stat'd as a regular file cannot be swapped for a link
// between the check and the open. O_NONBLOCK also guards against a FIFO,
// socket, or device swapped in at the path: open of a FIFO read end blocks
// until a writer opens it, so the nonblocking open returns immediately; the
// fstat then lets us reject anything that is not a regular file before any
// blocking read. O_NONBLOCK is cleared on the returned descriptor so callers
// read normally.
func openRegularRead(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		_ = unix.Close(fd)
		return nil, &os.PathError{Op: "fstat", Path: path, Err: err}
	}
	if uint32(st.Mode)&unix.S_IFMT != unix.S_IFREG {
		_ = unix.Close(fd)
		return nil, &os.PathError{Op: "open", Path: path, Err: fmt.Errorf("not a regular file")}
	}
	if err := unix.SetNonblock(fd, false); err != nil {
		_ = unix.Close(fd)
		return nil, &os.PathError{Op: "setnonblock", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}

// openRegularWrite creates or truncates a regular file for writing without
// following a final symlink: a pre-placed symlink at the destination is refused
// instead of being followed, so the copy cannot be redirected elsewhere.
func openRegularWrite(path string, perm uint32) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_WRONLY|unix.O_CREAT|unix.O_TRUNC|unix.O_NOFOLLOW|unix.O_CLOEXEC, perm)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}
