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
	// The error this produces tells the operator their original is preserved at
	// oldPath. Record that so the statement stays true: a later run finds
	// targetPath occupied — by whatever won the gap — and would otherwise treat
	// oldPath as an ordinary leftover and delete the only known-good copy.
	markOldBinaryPreserved(oldPath)
	return fmt.Errorf("%w: %v", ErrTargetPossiblyTampered, err)
}

// oldBinaryPreservedSuffix names the marker written beside a "<path>.old" that
// a failed restore left as the last known-good binary.
const oldBinaryPreservedSuffix = ".keep"

// markOldBinaryPreserved records that oldPath must survive routine cleanup. It
// is best-effort by nature: the marker lives in the same directory as the binary
// and anyone who can write there can remove it, which only returns cleanup to
// its previous behavior. It is a note to the next run, not a security control.
func markOldBinaryPreserved(oldPath string) {
	marker, err := os.OpenFile(oldPath+oldBinaryPreservedSuffix, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return
	}
	_, _ = marker.WriteString("The update at this path failed and could not restore the original binary.\n" +
		"The file without this marker's .keep suffix is the last binary this updater verified.\n")
	_ = marker.Close()
}

// clearOldBinaryPreserved drops the marker once a promotion has succeeded: the
// installed binary is verified again, so the preserved copy is an ordinary
// leftover and normal cleanup should reclaim it.
func clearOldBinaryPreserved(oldPath string) {
	_ = os.Remove(oldPath + oldBinaryPreservedSuffix)
}

// oldBinaryPreserved reports whether a failed restore marked oldPath as the last
// known-good binary.
func oldBinaryPreserved(oldPath string) bool {
	_, err := os.Lstat(oldPath + oldBinaryPreservedSuffix)
	return err == nil
}

// CleanupStaleBinary best-effort removes the known "<path>.old" copy, but only
// after confirming targetPath exists. If targetPath is absent or cannot be
// inspected, .old may be the only known-good binary left by an interrupted
// promotion and is preserved. Random staging files are also preserved because
// their public name is not proof that this updater created them.
//
// A present targetPath is not by itself proof the .old copy is disposable. When
// a promotion failed AND the restore failed, what occupies targetPath is exactly
// what the updater could not verify, and .old holds the binary it could — so a
// marker left by that path keeps both until an update succeeds. Deleting .old
// there would destroy the recovery copy the failure told the operator to use.
func CleanupStaleBinary(targetPath string) {
	if _, err := os.Lstat(targetPath); err != nil {
		return
	}
	oldPath := targetPath + ".old"
	if oldBinaryPreserved(oldPath) {
		return
	}
	_ = os.Remove(oldPath)
}
