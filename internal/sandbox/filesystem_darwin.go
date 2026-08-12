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
