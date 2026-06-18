//go:build !windows

package oauth

func isTransientSecretAccessError(error) bool {
	return false
}
