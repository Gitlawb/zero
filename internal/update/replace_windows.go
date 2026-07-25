//go:build windows

package update

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
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
// targetPath after a failed promotion. If the immediate rename-with-retry
// cannot get past whatever now occupies targetPath, it also asks Windows to
// perform the same replacement at the next boot (MOVEFILE_DELAY_UNTIL_REBOOT):
// that operation runs very early during startup, before most user-mode
// processes — including whatever is holding the lock this attempt could not
// get past — have a chance to run again, so it can recover cases an
// immediate retry cannot. Scheduling it requires administrator context and is
// best-effort: silently skipped rather than treated as a further failure if
// this process cannot register one.
func restoreOriginalBinary(oldPath string, targetPath string) error {
	err := renameWithRetry(oldPath, targetPath)
	if err == nil {
		return nil
	}
	if scheduleErr := scheduleRenameOnReboot(oldPath, targetPath); scheduleErr == nil {
		return fmt.Errorf("%w: restoration scheduled for the next reboot (immediate attempt failed: %v)", ErrTargetPossiblyTampered, err)
	}
	return fmt.Errorf("%w: %v", ErrTargetPossiblyTampered, err)
}

// scheduleRenameOnReboot registers oldPath to replace targetPath the next
// time Windows starts, via the same PendingFileRenameOperations mechanism
// installers use to replace files that are in use.
func scheduleRenameOnReboot(oldPath string, targetPath string) error {
	from, err := windows.UTF16PtrFromString(oldPath)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(targetPath)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_DELAY_UNTIL_REBOOT)
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
	targetBase := filepath.Base(targetPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !isGeneratedStagingFileName(targetBase, entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil || now.Sub(info.ModTime()) < stagingLeftoverMinAge {
			continue
		}
		// Re-check identity immediately before deleting: entry.Info() (and the
		// ReadDir that produced entry) can be arbitrarily old by the time this
		// loop reaches it, and a principal who can write in dir could have
		// swapped path for something else in the meantime. This shrinks the
		// window from "since the last sweep" to the gap between these two
		// syscalls.
		path := filepath.Join(dir, entry.Name())
		recheck, err := os.Lstat(path)
		if err != nil || !os.SameFile(info, recheck) {
			continue
		}
		_ = os.Remove(path)
	}
}

// stagingRandomSuffixHexLen is the length of stagingFilePath's random
// component: hex.EncodeToString of 16 random bytes is always exactly 32
// lowercase hex characters.
const stagingRandomSuffixHexLen = 32

// isGeneratedStagingFileName reports whether name is exactly the shape
// stagingFilePath generates for targetBase: "<targetBase>.<32 lowercase hex
// chars>.new". Matching only this exact shape, not just the prefix/suffix,
// keeps a user's own similarly-named file (e.g. "zero.exe.release-notes.new")
// from ever being swept up as an abandoned artifact.
func isGeneratedStagingFileName(targetBase string, name string) bool {
	rest, ok := strings.CutPrefix(name, targetBase+".")
	if !ok {
		return false
	}
	suffix, ok := strings.CutSuffix(rest, ".new")
	if !ok || len(suffix) != stagingRandomSuffixHexLen {
		return false
	}
	for _, r := range suffix {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}
