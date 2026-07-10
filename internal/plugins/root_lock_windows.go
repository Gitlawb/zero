//go:build windows

package plugins

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	pluginRootLockFileName = ".plugins-root.lock"
	pluginRootLockStaleAge = 30 * time.Minute
)

type pluginRootLock struct {
	path string
	file *os.File
}

func acquirePluginRootLock(dir string) (*pluginRootLock, error) {
	lockPath := filepath.Join(dir, pluginRootLockFileName)
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			if recoverStalePluginRootLock(lockPath) {
				return acquirePluginRootLock(dir)
			}
			return nil, fmt.Errorf("plugins root is locked")
		}
		return nil, fmt.Errorf("acquire plugins root lock: %w", err)
	}
	if _, err := file.WriteString(strconv.Itoa(os.Getpid()) + "\n"); err != nil {
		_ = file.Close()
		_ = os.Remove(lockPath)
		return nil, fmt.Errorf("write plugins root lock: %w", err)
	}
	return &pluginRootLock{path: lockPath, file: file}, nil
}

func recoverStalePluginRootLock(lockPath string) bool {
	info, err := os.Stat(lockPath)
	if err != nil {
		return errors.Is(err, os.ErrNotExist)
	}
	if time.Since(info.ModTime()) < pluginRootLockStaleAge {
		return false
	}
	data, err := os.ReadFile(lockPath)
	if err == nil {
		pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		if pid > 0 {
			if process, err := os.FindProcess(pid); err == nil {
				_ = process.Release()
			}
		}
	}
	return os.Remove(lockPath) == nil
}

func (lock *pluginRootLock) release() {
	if lock == nil || lock.file == nil {
		return
	}
	_ = lock.file.Close()
	_ = os.Remove(lock.path)
}
