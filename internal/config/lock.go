package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Gitlawb/zero/internal/lockutil"
)

const (
	configLockTimeout    = 10 * time.Second
	configLockRetryDelay = 20 * time.Millisecond
)

// lockConfigFile serializes a config read-modify-write across processes.
//
// Every mutator in this package loads the whole document, edits independent
// fields, and publishes a complete replacement by rename. The rename is atomic,
// so a reader never sees partial JSON — but two processes that both loaded the
// same revision each write a full document, and the second rename silently
// discards the first one's acknowledged update (issue #832). Startup work makes
// this easy to hit: MigratePlaintextProviderKeys rewrites the config on every
// launch, so it collides with any concurrent mutation from another Zero.
//
// The lock must therefore span load, mutation, validation AND publication:
// holding it only around the write would still let both processes read the same
// stale revision first. Callers acquire before their first read, which is what
// makes the re-read inside the lock authoritative.
//
// The lock file is a sibling of the config, never the config itself. The kernel
// holds an advisory lock against an inode, and publishing the config by rename
// installs a NEW inode — locking the config directly would leave each process
// holding a different one. lockutil keeps the sibling's path stable for the
// same reason, and never removes it.
//
// This also serializes goroutines within one process: each acquisition opens
// its own file description, so a second in-process attempt contends exactly as
// another process would. The lock is NOT reentrant, so an exported mutator that
// needs another mutator's work calls the unexported *Locked form instead of
// re-entering through the public function.
func lockConfigFile(path string) (func(), error) {
	lockPath := path + ".lock"
	if dir := filepath.Dir(lockPath); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create config directory %s: %w", dir, err)
		}
	}
	deadline := time.Now().Add(configLockTimeout)
	for {
		lock, err := lockutil.TryAcquireFileLock(lockPath)
		if err == nil {
			return func() { _ = lock.Release() }, nil
		}
		if !errors.Is(err, lockutil.ErrLockHeld) {
			return nil, fmt.Errorf("config: acquire config lock: %w", err)
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("config: timed out acquiring config lock for %s", path)
		}
		time.Sleep(configLockRetryDelay)
	}
}
