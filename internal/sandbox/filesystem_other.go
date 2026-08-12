//go:build !darwin

package sandbox

func pathsShareFilesystem(_, _ string) bool {
	return false
}
