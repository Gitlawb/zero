//go:build windows

package update

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	// oldPath. Record that so a later promotion refuses to treat the recovery
	// copy as the ordinary destination for another aside rename.
	if markErr := markOldBinaryPreserved(oldPath); markErr != nil {
		// The marker could not be established, so nothing on disk identifies
		// oldPath as the recovery copy. Move it to a distinct name and report that
		// authoritative location to the operator.
		if kept, keepErr := keepUnmarkedRecoveryCopy(oldPath); keepErr == nil {
			return fmt.Errorf(
				"%w: %v (the recovery marker could not be written: %v; the last binary this updater verified was moved to the distinct recovery path %s)",
				ErrTargetPossiblyTampered, err, markErr, kept,
			)
		}
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
// and nothing worth opening a pre-existing object for. That also means an entry
// planted at the name by someone else reads as marked, which is the fail-safe
// direction: it makes the next run PRESERVE the recovery copy.
//
// A var so a test can force the failure branch. Every way to make the real
// CreateFile fail here (an unwritable directory, an over-length name) either
// takes the recovery copy down with it or is too platform-fragile to assert on.
var markOldBinaryPreserved = func(oldPath string) error {
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

// keepUnmarkedRecoveryCopy moves oldPath out from under routine cleanup when its
// marker could not be established, returning the path it now lives at.
//
// The name is unpredictable for the same reason the staging name is: this runs
// in a directory a lower-privileged principal may be able to write, and a fixed
// recovery name could be pre-created there to make this rename fail or land
// somewhere chosen by someone else. Being unpredictable also means being opaque,
// which is why the caller's error names the path.
func keepUnmarkedRecoveryCopy(oldPath string) (string, error) {
	suffix, err := randomStagingSuffix()
	if err != nil {
		return "", err
	}
	kept := filepath.Join(filepath.Dir(oldPath), filepath.Base(oldPath)+"."+suffix+".recovery")
	if _, statErr := os.Lstat(kept); statErr == nil {
		return "", fmt.Errorf("recovery path %s already exists", kept)
	}
	if err := os.Rename(oldPath, kept); err != nil {
		return "", err
	}
	return kept, nil
}

// clearOldBinaryPreserved models the operator accepting the installed binary
// by deleting the recovery marker. The recovery copy itself remains preserved.
func clearOldBinaryPreserved(oldPath string) {
	_ = os.Remove(oldPath + oldBinaryPreservedSuffix)
}

// oldBinaryPreserved reports whether a failed restore marked oldPath as the last
// known-good binary.
//
// Only a definite "the marker is not there" answers false. An Lstat that fails
// for any other reason (permissions, a transient sharing error) leaves the
// question open, and the conservative answer to an open question here is to keep
// the copy: deleting it is irreversible, while keeping it costs one stale file.
func oldBinaryPreserved(oldPath string) bool {
	_, err := os.Lstat(oldPath + oldBinaryPreservedSuffix)
	return err == nil || !errors.Is(err, os.ErrNotExist)
}

// CleanupStaleBinary intentionally preserves Windows recovery copies. A public
// pathname cannot prove that an .old file is obsolete under the writable-install-
// directory threat model: a deleted .keep marker, an interrupted promotion, or
// an operator-approved retry can all leave .old as the last verified binary.
// Safe bounded cleanup would require trusted state outside that directory.
func CleanupStaleBinary(string) {}
