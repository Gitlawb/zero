//go:build windows

package update

import (
	"errors"
	"fmt"
	"os"
	"time"
)

const (
	restoreRenameRetryAttempts = 10
	restoreRenameRetryDelay    = 100 * time.Millisecond
)

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

// ErrTargetPossiblyTampered is wrapped into the error promote returns when a
// promotion attempt fails AND restoring the original binary to targetPath
// also fails. That combination means a principal who can write in the
// installation directory occupied targetPath in the gap the updater opened by
// renaming the running binary aside, and Windows would not let anything —
// including the restore — replace it: MOVEFILE_REPLACE_EXISTING cannot force
// past another handle's share-mode lock. This is not an ordinary failed
// update (the previous version simply staying in place); the executable path
// may now hold attacker-controlled bytes, so a caller must surface it as a
// security-relevant condition, not the same "try again later" failure as a
// stalled download.
var ErrTargetPossiblyTampered = errors.New("target executable path may hold unverified content after a failed update")

// restoreOriginalBinary moves the preserved original at oldPath back onto
// targetPath after a failed promotion. A failed immediate restore is surfaced;
// oldPath must not be queued as a reboot source because its pathname can be
// replaced before reboot under the writable-directory threat model.
func restoreOriginalBinary(oldPath string, targetPath string) error {
	err := renameWithRetry(oldPath, targetPath)
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %v", ErrTargetPossiblyTampered, err)
}

// CleanupStaleBinary best-effort removes the known "<path>.old" copy, but only
// after confirming targetPath exists. If targetPath is absent or cannot be
// inspected, .old may be the only known-good binary left by an interrupted
// promotion and is preserved. Random staging files are also preserved because
// their public name is not proof that this updater created them.
func CleanupStaleBinary(targetPath string) {
	if _, err := os.Lstat(targetPath); err != nil {
		return
	}
	_ = os.Remove(targetPath + ".old")
}
