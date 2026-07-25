//go:build windows

package update

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	restoreRenameRetryAttempts = 10
	restoreRenameRetryDelay    = 100 * time.Millisecond
)

// stagingLeftoverMinAge is how long a staging leftover must sit untouched before
// it is treated as abandoned rather than as another process's work in progress.
const stagingLeftoverMinAge = time.Hour

func renameWithRetry(oldPath string, newPath string) error {
	var lastErr error
	for attempt := 0; attempt < restoreRenameRetryAttempts; attempt++ {
		if err := os.Rename(oldPath, newPath); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(restoreRenameRetryDelay)
	}
	return lastErr
}

// CleanupStaleBinary best-effort removes what a previous update left next to
// targetPath: the "<path>.old" copy of the running binary (removable once the
// process holding it has exited) and any staging file abandoned by a crashed or
// killed update. The staging name is random, so nothing else would ever reclaim
// it — each crashed attempt would otherwise leave another release-sized file
// behind. Callers invoke this once at startup for the current executable.
func CleanupStaleBinary(targetPath string) {
	_ = os.Remove(targetPath + ".old")
	removeStaleStagingLeftovers(targetPath, time.Now())
}

// removeStaleStagingLeftovers deletes "<base>.<random>.new" files older than
// stagingLeftoverMinAge, so a staging file belonging to a concurrently running
// update is never pulled out from under it.
func removeStaleStagingLeftovers(targetPath string, now time.Time) {
	dir := filepath.Dir(targetPath)
	prefix := filepath.Base(targetPath) + "."
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".new") {
			continue
		}
		info, err := entry.Info()
		if err != nil || now.Sub(info.ModTime()) < stagingLeftoverMinAge {
			continue
		}
		_ = os.Remove(filepath.Join(dir, name))
	}
}
