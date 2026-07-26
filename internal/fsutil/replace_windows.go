//go:build windows

package fsutil

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// replaceFileFlags is deliberately ZERO. Every REPLACEFILE_* flag ReplaceFileW
// accepts either defeats the reason this function exists or does nothing:
//
//   - REPLACEFILE_IGNORE_MERGE_ERRORS (0x2) and REPLACEFILE_IGNORE_ACL_ERRORS
//     (0x4). Microsoft documents BOTH with the same consequence: "if you specify
//     this flag and do not have WRITE_DAC access, the function succeeds but the
//     ACLs are not preserved." A silent success that publishes the temporary
//     file's inherited directory DACL over an explicitly restricted specialist —
//     exposing its system prompt — is precisely the failure this function exists
//     to prevent, so a merge failure MUST surface as an error and leave the
//     destination untouched. Passing 0x2 while omitting only 0x4 buys nothing:
//     ACL merging is part of the metadata merge 0x2 covers.
//   - REPLACEFILE_WRITE_THROUGH (0x1) is documented as "This value is not
//     supported", so it cannot be relied on to flush anything. The single-
//     operation swap is ReplaceFileW's own documented behavior, not this flag's.
const replaceFileFlags = 0

const (
	// Returned when the volume cannot perform a replace (FAT/exFAT, some network
	// redirectors). Such volumes carry no ACL to preserve, so a plain rename is
	// an equivalent publish there.
	errorInvalidFunction = syscall.Errno(1)
	errorNotSupported    = syscall.Errno(50)

	// Partial-failure codes: ReplaceFileW got far enough to move or delete
	// something, so the on-disk state needs repair rather than a bare error. See
	// recoverPartialReplace.
	errorUnableToRemoveReplaced   = syscall.Errno(1175)
	errorUnableToMoveReplacement  = syscall.Errno(1176)
	errorUnableToMoveReplacement2 = syscall.Errno(1177)
)

var (
	replaceKernel32       = syscall.NewLazyDLL("kernel32.dll")
	replaceProcReplaceFil = replaceKernel32.NewProc("ReplaceFileW")
)

// replaceExisting publishes src over dst with ReplaceFileW rather than
// MoveFileEx (what os.Rename uses). Two reasons, both of which os.Rename fails
// to provide on Windows:
//
//   - Atomicity. Go documents os.Rename as NOT atomic outside Unix, so an
//     interrupted or concurrently observed overwrite can expose a missing or
//     intermediate file. ReplaceFileW performs the swap as one operation.
//   - Security descriptor. The replacement is a freshly created temporary file,
//     so it carries the directory's inherited DACL. Renaming it over the
//     destination therefore REPLACES the destination's ACL — silently widening
//     access to a file that had been restricted explicitly (os.File.Chmod cannot
//     express that on Windows; Go only maps the owner-write bit). ReplaceFileW
//     carries the replaced file's security descriptor, attributes, and streams
//     over to the replacement instead.
//
// No REPLACEFILE_* flag is passed at all — see replaceFileFlags for why each one
// would either silently lose the descriptor this function exists to preserve or
// do nothing. A merge failure therefore surfaces as an error, leaving the
// destination untouched and the caller free to clean up its temporary file,
// except in the partial-failure states recoverPartialReplace repairs.
func replaceExisting(src, dst string) error {
	if _, err := os.Lstat(dst); err != nil {
		if os.IsNotExist(err) {
			// Nothing to replace and no descriptor to preserve.
			return os.Rename(src, dst)
		}
		return err
	}
	replaced, err := syscall.UTF16PtrFromString(dst)
	if err != nil {
		return err
	}
	replacement, err := syscall.UTF16PtrFromString(src)
	if err != nil {
		return err
	}
	result, _, callErr := replaceProcReplaceFil.Call(
		uintptr(unsafe.Pointer(replaced)),
		uintptr(unsafe.Pointer(replacement)),
		0, // no backup copy
		uintptr(replaceFileFlags),
		0,
		0,
	)
	if result != 0 {
		return nil
	}
	if callErr == nil || errors.Is(callErr, syscall.Errno(0)) {
		return fmt.Errorf("replace %s: ReplaceFileW failed", dst)
	}
	if errors.Is(callErr, errorInvalidFunction) || errors.Is(callErr, errorNotSupported) {
		return os.Rename(src, dst)
	}
	return recoverPartialReplace(callErr, src, dst)
}

// recoverPartialReplace repairs the on-disk state after a ReplaceFileW failure
// that had already moved or deleted something, then returns the error to report.
//
// We pass no backup file, and Microsoft documents that this makes two failure
// codes leave the DESTINATION GONE while the replacement survives only under its
// original (temporary) name:
//
//   - ERROR_UNABLE_TO_MOVE_REPLACEMENT (1176): "If lpBackupFileName was
//     specified, the replaced and replacement files retain their original file
//     names. Otherwise, the replaced file no longer exists and the replacement
//     file exists under its original name."
//   - ERROR_UNABLE_TO_MOVE_REPLACEMENT_2 (1177): the replacement survives under
//     its original name having already inherited the replaced file's streams and
//     attributes, while the replaced file "still exists with a different name" —
//     a name only the backup parameter would have reported, so with no backup it
//     is unreachable and the destination path is equally empty.
//
// Callers write to a temporary file and remove it unconditionally on failure
// (internal/specialist's writeSpecialistAtomicWith does, via defer), so
// returning either error unrepaired destroys BOTH copies: the destination
// Windows already deleted, and the only surviving copy of the new content. A
// failed `specialist create --force` would leave no manifest at all.
//
// Moving the replacement into place is therefore the recovery. The destination
// path is free — Windows deleted or renamed away whatever was there — so the new
// content lands where it belongs and nothing is left for the caller's cleanup to
// delete. The error is still returned rather than swallowed: the destination's
// original security descriptor died with the destination, so the published file
// carries the temporary file's inherited DACL, and reporting success would be
// exactly the silent ACL widening replaceFileFlags refuses to allow.
func recoverPartialReplace(callErr error, src, dst string) error {
	if !errors.Is(callErr, errorUnableToMoveReplacement) && !errors.Is(callErr, errorUnableToMoveReplacement2) {
		// Everything else — including ERROR_UNABLE_TO_REMOVE_REPLACED (1175) and
		// every unlisted error, for which Microsoft documents that "the replaced and
		// replacement files retain their original file names" — leaves the
		// destination holding its original content. The caller's temporary-file
		// cleanup is then correct and there is nothing to repair.
		return callErr
	}
	if _, err := os.Lstat(src); err != nil {
		// No replacement left to recover with; report the original failure.
		return callErr
	}
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("replace %s: %w (the replacement survives at %s; recovering it failed: %v)", dst, callErr, src, err)
	}
	return fmt.Errorf("replace %s: %w (the replacement was moved into place, but the destination's security descriptor was lost with the destination — re-check its permissions)", dst, callErr)
}
