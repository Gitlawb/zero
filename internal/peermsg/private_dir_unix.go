//go:build !windows

package peermsg

import (
	"fmt"
	"os"
	"syscall"
)

func ensurePrivateDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("refusing non-directory or symlink runtime path %q", path)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("refusing runtime directory not owned by the current user: %q", path)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return err
	}
	return nil
}
