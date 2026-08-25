//go:build !windows

package daemon

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func checkStatusDirOwner(_ *os.Root, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("status directory ownership metadata is unavailable")
	}
	if int(stat.Uid) != os.Geteuid() {
		return fmt.Errorf("status directory is owned by uid %d, not the current user", stat.Uid)
	}
	return nil
}

func hardenStatusDir(root *os.Root) (returnErr error) {
	directory, err := root.Open(".")
	if err != nil {
		return fmt.Errorf("open status directory for hardening: %w", err)
	}
	defer func() {
		if err := directory.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close status directory hardening handle: %w", err))
		}
	}()
	info, err := directory.Stat()
	if err != nil {
		return fmt.Errorf("inspect status directory ownership before hardening: %w", err)
	}
	if err := checkStatusDirOwner(root, info); err != nil {
		return err
	}
	if err := directory.Chmod(0o700); err != nil {
		return fmt.Errorf("harden status directory permissions: %w", err)
	}
	return nil
}
