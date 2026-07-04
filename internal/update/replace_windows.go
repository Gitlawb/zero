//go:build windows

package update

import (
	"fmt"
	"os"
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
		if restoreErr := os.Rename(oldPath, targetPath); restoreErr != nil {
			return fmt.Errorf("install new binary: %w; additionally failed to restore the original binary: %v (original preserved at %s)", err, restoreErr, oldPath)
		}
		return fmt.Errorf("install new binary: %w", err)
	}
	return nil
}

// CleanupStaleBinary best-effort removes a "<path>.old" file left behind by a
// previous replaceBinary call once the old process holding it has exited.
// Callers should invoke this once at startup for the current executable.
func CleanupStaleBinary(targetPath string) {
	_ = os.Remove(targetPath + ".old")
}
