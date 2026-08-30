package tools

import (
	"errors"
	"os"

	"github.com/Gitlawb/zero/internal/fsutil"
)

// committedWrite publishes data via WriteFileAtomic. A committed replacement
// whose backup cleanup failed is treated as a successful write; the warning is
// returned for the caller to surface without flipping the tool status to error.
func committedWrite(path string, data []byte, perm os.FileMode) (string, error) {
	err := fsutil.WriteFileAtomic(path, data, perm)
	if err == nil {
		return "", nil
	}
	var committed *fsutil.CommittedReplacementCleanupError
	if errors.As(err, &committed) {
		return "replacement committed, but backup cleanup failed", nil
	}
	return "", err
}
