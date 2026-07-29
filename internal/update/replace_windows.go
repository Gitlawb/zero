//go:build windows

package update

import (
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"
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

// This file is where ErrTargetPossiblyTampered (declared in apply.go, so
// cross-platform callers can test for it) is produced. It is wrapped into the
// error promote returns when a promotion attempt fails AND restoring the
// original binary to targetPath also fails. That combination means a principal
// who can write in the installation directory occupied targetPath in the gap
// the updater opened by renaming the running binary aside, and Windows would
// not let anything — including the restore — replace it:
// MOVEFILE_REPLACE_EXISTING cannot force past another handle's share-mode lock.
// This is not an ordinary failed update (the previous version simply staying in
// place); the executable path may now hold attacker-controlled bytes, so a
// caller must surface it as a security-relevant condition, not the same "try
// again later" failure as a stalled download.
//
// It is also returned by promote when a PREVIOUS run left that state behind and
// nobody has resolved it yet — see the refusal there.

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
	if markErr := markOldBinaryPreserved(oldPath); markErr != nil {
		// Do not let the promise go out unqualified. Without the marker, the next
		// run's cleanup sees a present target and removes oldPath, so the operator
		// has to act now rather than at their convenience.
		return fmt.Errorf(
			"%w: %v (the recovery marker could not be written: %v — copy %s somewhere safe now, a later update will otherwise remove it)",
			ErrTargetPossiblyTampered, err, markErr, oldPath,
		)
	}
	return fmt.Errorf("%w: %v", ErrTargetPossiblyTampered, err)
}

// oldBinaryPreservedSuffix names the marker written beside a "<path>.old" that
// a failed restore left as the last known-good binary.
const oldBinaryPreservedSuffix = ".keep"

// markOldBinaryPreserved records that oldPath must survive routine cleanup.
//
// The marker is created with the same exclusive, no-follow semantics as a
// staging file, and never truncates. Its pathname is predictable, so under the
// writable-install-directory threat model a lower-privileged writer can
// pre-create it as a hard link or reparse point; opening that with O_TRUNC
// would let the elevated updater write through it into a file of the attacker's
// choosing. CREATE_NEW plus FILE_FLAG_OPEN_REPARSE_POINT plus the same
// fresh-regular-file check refuses that object instead of writing to it.
//
// An existing marker is success, not a rewrite: its contents carry no state
// beyond "this .old is the last verified binary", so there is nothing to update
// and nothing worth opening a pre-existing object for.
func markOldBinaryPreserved(oldPath string) error {
	markerPath := oldPath + oldBinaryPreservedSuffix
	if _, err := os.Lstat(markerPath); err == nil {
		return nil
	}
	pathPtr, err := windows.UTF16PtrFromString(markerPath)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_WRITE,
		0,
		nil,
		windows.CREATE_NEW,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_EXISTS) || errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			// Something already occupies the marker name. Whatever it is, this
			// function's only job is to make the next run preserve oldPath, and a
			// present name does that — without this process writing through an
			// object it did not create.
			return nil
		}
		return fmt.Errorf("create recovery marker %s: %w", markerPath, err)
	}
	if err := verifyFreshRegularFile(handle, markerPath); err != nil {
		_ = windows.CloseHandle(handle)
		_ = os.Remove(markerPath)
		return err
	}
	marker := os.NewFile(uintptr(handle), markerPath)
	_, writeErr := marker.WriteString("The update at this path failed and could not restore the original binary.\n" +
		"The file without this marker's .keep suffix is the last binary this updater verified.\n")
	closeErr := marker.Close()
	if writeErr != nil {
		return fmt.Errorf("write recovery marker %s: %w", markerPath, writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close recovery marker %s: %w", markerPath, closeErr)
	}
	return nil
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
