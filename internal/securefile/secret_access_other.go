//go:build !windows

package securefile

func isTransientSecretAccessError(error) bool {
	return false
}
