//go:build !unix && !windows

package fscopy

import (
	"os"
)

// No no-follow primitive exists on this platform (the file only compiles where
// //go:build !unix && !windows holds, i.e. Plan 9 and WASM). The sibling files
// open_unix.go (O_NOFOLLOW) and open_windows.go (FILE_FLAG_OPEN_REPARSE_POINT)
// use the platform-native no-follow primitive; this file is the "no native
// no-follow" branch of that dispatch.
//
// Per the no-follow contract — "if a native no-follow primitive is available,
// use it; otherwise fail closed rather than best-effort" — both helpers here
// refuse to open. The earlier TOCTOU-narrowing approach (Lstat before/after a
// plain open, or an O_TRUNC-free write plus a post-open fd fstat) reduced but
// did NOT close the race: with no atomic open-time refusal of a final-component
// symlink, an attacker can swap a symlink into the path between the check and
// the open, and the truncation (or the followed read through the link's target)
// lands before any post-open re-check can refuse it. Fail closed: never open,
// never truncate, never follow. An install/hashing operation that cannot
// guarantee a no-follow open is not safe to run on these targets.

// openRegularRead opens a regular file for reading without following a final
// symlink. No no-follow primitive is available on this platform, so it fails
// closed rather than risk following a swapped-in symlink.
func openRegularRead(path string) (*os.File, error) {
	return nil, &os.PathError{Op: "open", Path: path, Err: errNoNoFollow}
}

// openRegularWrite creates or truncates a regular file for writing without
// following a final symlink. No no-follow primitive is available on this
// platform, so it fails closed rather than risk truncating a swapped-in symlink
// target (an irreversible operation) or writing into one.
func openRegularWrite(path string, perm uint32) (*os.File, error) {
	return nil, &os.PathError{Op: "open", Path: path, Err: errNoNoFollow}
}

// errNoNoFollow is the closed failure returned when no platform-native
// no-follow open primitive is available. It is a sentinel so callers/tests can
// distinguish "unsupported target" from an ordinary I/O error.
var errNoNoFollow = os.ErrInvalid
