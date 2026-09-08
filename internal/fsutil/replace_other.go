//go:build !windows

package fsutil

import "os"

// replaceExisting publishes src over dst. rename(2) already replaces the
// destination atomically within one filesystem on Unix, and it neither creates
// nor consults an ACL, so there is nothing extra to preserve here.
func replaceExisting(src, dst string) error {
	return os.Rename(src, dst)
}
