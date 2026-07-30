//go:build !windows

package credstore

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// acquireFileLock takes an exclusive advisory lock (flock) so a read-modify-write
// of the credential file is serialized against every other one — across
// processes AND across goroutines, since flock is held per open file
// description and two opens in one process contend exactly as two processes do.
//
// THE LOCK FILE IS SEPARATE FROM THE DATA FILE, and that is not tidiness. write
// publishes by os.Rename, which replaces the inode; a lock taken on the data
// file would be attached to an inode that the next writer has already replaced,
// so every writer would appear to hold it. The lock lives on a file nothing
// renames.
func (s *Store) acquireFileLock() (func(), error) {
	path := s.lockPath()
	if err := os.MkdirAll(filepathDir(path), 0o700); err != nil {
		return nil, fmt.Errorf("credstore: lock dir: %w", err)
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("credstore: open lock: %w", err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("credstore: lock: %w", err)
	}
	return func() {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
	}, nil
}
