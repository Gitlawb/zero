//go:build !unix && !windows

package peermsg

import (
	"fmt"
	"os"
)

func ensurePrivateDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing non-directory or symlink runtime path %q", path)
	}
	return os.Chmod(path, 0o700)
}
