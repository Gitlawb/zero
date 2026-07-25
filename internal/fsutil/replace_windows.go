//go:build windows

package fsutil

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

const (
	replaceFileWriteThrough      = 0x00000001
	replaceFileIgnoreMergeErrors = 0x00000002

	// Returned when the volume cannot perform a replace (FAT/exFAT, some network
	// redirectors). Such volumes carry no ACL to preserve, so a plain rename is
	// an equivalent publish there.
	errorInvalidFunction = syscall.Errno(1)
	errorNotSupported    = syscall.Errno(50)
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
//     intermediate file. ReplaceFileW performs the swap as one operation, and
//     REPLACEFILE_WRITE_THROUGH flushes it before returning.
//   - Security descriptor. The replacement is a freshly created temporary file,
//     so it carries the directory's inherited DACL. Renaming it over the
//     destination therefore REPLACES the destination's ACL — silently widening
//     access to a file that had been restricted explicitly (os.File.Chmod cannot
//     express that on Windows; Go only maps the owner-write bit). ReplaceFileW
//     carries the replaced file's security descriptor, attributes, and streams
//     over to the replacement instead.
//
// REPLACEFILE_IGNORE_ACL_ERRORS is deliberately NOT passed: silently losing the
// descriptor is the very failure this exists to prevent, so an ACL merge failure
// surfaces as an error, leaving the destination untouched and the caller free to
// clean up its temporary file.
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
		uintptr(replaceFileWriteThrough|replaceFileIgnoreMergeErrors),
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
	return callErr
}
