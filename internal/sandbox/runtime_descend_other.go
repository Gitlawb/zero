//go:build !windows

package sandbox

import (
	"fmt"
	"os"
)

// runtimeDescentBarrier is the Windows test seam; nothing consults it here, but
// the name exists on every platform so a test that sets it compiles everywhere.
var runtimeDescentBarrier func()

// createRuntimeTailHandleRelative keeps the pathname form off Windows. The
// elevated-installer-versus-unelevated-renamer split the handle-relative
// descent closes does not apply here, so this is the mkdir loop it replaced,
// creating outermost-first and recording each component it made.
func createRuntimeTailHandleRelative(base string, tail []string) ([]windowsCreatedRuntimeDir, error) {
	_ = base
	var created []windowsCreatedRuntimeDir
	for _, path := range tail {
		if err := os.Mkdir(path, 0o700); err != nil {
			if os.IsExist(err) {
				continue
			}
			return created, fmt.Errorf("create sandbox runtime root %s: %w", path, err)
		}
		identity, ok := runtimeIdentityAfterCreate(path)
		created = append(created, windowsCreatedRuntimeDir{path: path, identity: identity, identified: ok})
	}
	return created, nil
}
