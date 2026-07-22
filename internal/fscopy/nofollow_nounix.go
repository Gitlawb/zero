//go:build !unix && !windows

package fscopy

import "os"

// noFollowOpenDir opens the directory named by path. On targets without a
// native no-follow directory-open primitive, the traversal falls back to
// pathname-based os.ReadDir (see CopyTree/hashTreeInto in fscopy.go). This
// helper exists so the cross-platform CopyTree/HashTree entry points can share
// a single shape; on these unsupported install targets it simply opens the dir
// for reading via os.Open.
func noFollowOpenDir(path string) (*os.File, error) {
	return os.Open(path)
}
