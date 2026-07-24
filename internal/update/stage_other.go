//go:build !windows

package update

import "os"

// createStagingFile creates path exclusively so a pre-existing hard link or
// symlink at that path (which a lower-privileged attacker may have staged in
// a writable installation directory) can never be opened through: per POSIX,
// O_CREAT|O_EXCL fails with EEXIST if path already exists — including a
// dangling symlink — without following it.
func createStagingFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
}
