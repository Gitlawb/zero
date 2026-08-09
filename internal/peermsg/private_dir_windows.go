//go:build windows

package peermsg

import "os"

func ensurePrivateDir(path string) error {
	return os.MkdirAll(path, 0o700)
}
