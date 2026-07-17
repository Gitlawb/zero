// Test seams: helpers only test code uses, kept out of the production binary.
package repomap

import (
	"io/fs"

	"github.com/Gitlawb/zero/internal/workspaceindex"
)

func handleWalkError(cleanRoot string, current string, entry fs.DirEntry, walkErr error, truncated *bool) (bool, error) {
	return workspaceindex.HandleWalkError(cleanRoot, current, entry, walkErr, truncated)
}
