//go:build darwin || linux

package sandbox

import "golang.org/x/sys/unix"

func pathsShareFilesystem(left, right string) bool {
	var leftStat, rightStat unix.Stat_t
	if err := unix.Stat(left, &leftStat); err != nil {
		return false
	}
	if err := unix.Stat(right, &rightStat); err != nil {
		return false
	}
	return leftStat.Dev == rightStat.Dev
}

// pathHardLinkCount reports the number of directory entries that name the file's
// inode. A count above one proves an alias already exists somewhere the planner
// cannot enumerate, so callers must fail closed rather than mask one pathname.
// The second result is false when the count cannot be determined.
func pathHardLinkCount(path string) (uint64, bool) {
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil {
		return 0, false
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return 0, false
	}
	return uint64(stat.Nlink), true
}
