//go:build !darwin && !linux

package sandbox

func pathsShareFilesystem(_, _ string) bool {
	return false
}

func pathHardLinkCount(_ string) (uint64, bool) {
	return 0, false
}
