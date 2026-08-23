//go:build darwin || linux

package sandbox

import (
	"os"

	"golang.org/x/sys/unix"
)

// pathsShareFilesystem reports whether two paths live on the same filesystem.
// The second result is false when either path cannot be inspected, because an
// uninspectable root is not evidence of separation: callers reason about where a
// hard link COULD be created, so an unknown answer must be treated as "could".
func pathsShareFilesystem(left, right string) (shared bool, known bool) {
	var leftStat, rightStat unix.Stat_t
	if err := unix.Stat(left, &leftStat); err != nil {
		return false, false
	}
	if err := unix.Stat(right, &rightStat); err != nil {
		return false, false
	}
	return leftStat.Dev == rightStat.Dev, true
}

// pathHardLinkCount reports the number of directory entries that name the file's
// inode. A count above one proves an alias already exists somewhere the planner
// cannot enumerate, so callers must fail closed rather than mask one pathname.
//
// A missing file returns fs.ErrNotExist and count 0: an absent token has no
// inode to alias, and the pathname reservation that survives rotation is the
// lexical rule's job, not this one's. Any other inspection failure is returned
// as an error so the caller fails closed instead of reading it as "no alias".
// A non-regular file reports 0 with no error: link counts on a directory or a
// symlink do not describe an alias for the token's contents.
func pathHardLinkCount(path string) (uint64, error) {
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil {
		return 0, &os.PathError{Op: "lstat", Path: path, Err: err}
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return 0, nil
	}
	return uint64(stat.Nlink), nil
}
