//go:build !windows

package sandbox

import (
	"os"
	"path/filepath"
)

// physicalTempDir returns os.TempDir with every symlink in its ancestry
// resolved, so the fallback anchor's parent chain is physical and only the
// anchor itself is a new component.
//
// peermsg.EnsurePrivateDir walks from the root with O_NOFOLLOW and refuses any
// component that is a link. That is right for the owned tail and wrong for the
// ancestors above it: on macOS os.TempDir lives under /var, which is a symlink
// to /private/var, so the first version of this fix refused every fallback on
// every Mac with "refusing non-directory or symlink runtime path component
// var". The constraint jatmn stated for #901 applies here identically:
// redirected cache and TEMP locations above the owned tail are the operator's
// business and must keep working; the restriction is on what Zero owns.
//
// EvalSymlinks is the correct resolver off Windows, where the only reparse
// shape is a symlink and it traverses them.
func physicalTempDir() (string, error) {
	return filepath.EvalSymlinks(os.TempDir())
}
