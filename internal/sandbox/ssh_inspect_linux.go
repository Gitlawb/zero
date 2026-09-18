package sandbox

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// O_PATH pins the actual object without opening a FIFO or device for I/O.
// Reopening its procfs descriptor after fstat keeps the inspected inode even
// when any ancestor or the final pathname is concurrently replaced.
func openSSHInspectionFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	pinned := os.NewFile(uintptr(fd), path)
	defer pinned.Close()
	return openPinnedSSHInspectionFile(pinned)
}

func openPinnedSSHInspectionFile(pinned *os.File) (*os.File, error) {
	info, err := pinned.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("SSH inspection requires a regular file")
	}
	return os.Open(fmt.Sprintf("/proc/self/fd/%d", pinned.Fd()))
}
