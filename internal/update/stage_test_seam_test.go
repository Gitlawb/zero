package update

import (
	"path/filepath"
	"testing"
)

// stubStageBinaryFailure makes stageBinary fail for targetPath (matched by base
// name, since installBinary is called with the real install path) and behave
// normally for everything else, restoring the original on cleanup.
//
// The staging location is created exclusively under an unpredictable name — a
// private directory on POSIX — so a test can no more occupy it than an attacker
// can. This seam is how a staging failure is exercised instead.
func stubStageBinaryFailure(t *testing.T, targetPath string, failure error) {
	t.Helper()
	original := stageBinary
	want := filepath.Base(targetPath)
	stageBinary = func(sourcePath string, target string) (*stagedBinary, error) {
		if filepath.Base(target) == want {
			return nil, failure
		}
		return original(sourcePath, target)
	}
	t.Cleanup(func() { stageBinary = original })
}
