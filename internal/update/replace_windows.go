//go:build windows

package update

import (
	"fmt"
	"os"
	"time"
)

const (
	restoreRenameRetryAttempts = 10
	restoreRenameRetryDelay    = 100 * time.Millisecond
)

// replaceBinary installs newPath over targetPath. Windows will not let a
// running executable be overwritten or deleted directly, but NTFS does allow
// renaming it aside — the same trick already used for locked config files in
// internal/cli/mcp_config.go's replaceMCPWritableConfigFile.
func replaceBinary(targetPath string, newPath string) error {
	oldPath := targetPath + ".old"
	_ = os.Remove(oldPath) // best-effort cleanup of a leftover from a previous upgrade
	if err := os.Rename(targetPath, oldPath); err != nil {
		return fmt.Errorf("rename running binary aside: %w", err)
	}
	if err := os.Rename(newPath, targetPath); err != nil {
		// Retry the restore: a transient Windows file lock (antivirus/indexer
		// scanning the just-renamed file, a lingering handle) can make a rename
		// fail momentarily, and here failure means targetPath is left missing
		// entirely rather than merely stale — worth a short retry to avoid that.
		if restoreErr := renameWithRetry(oldPath, targetPath); restoreErr != nil {
			return fmt.Errorf("install new binary: %w; additionally failed to restore the original binary: %v (original preserved at %s)", err, restoreErr, oldPath)
		}
		return fmt.Errorf("install new binary: %w", err)
	}
	return nil
}

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

// CleanupStaleBinary best-effort removes a "<path>.old" file left behind by a
// previous replaceBinary call once the old process holding it has exited.
// Callers should invoke this once at startup for the current executable.
func CleanupStaleBinary(targetPath string) {
	_ = os.Remove(targetPath + ".old")
}
