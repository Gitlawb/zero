//go:build !windows

package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
)

// acquireRuntimeLeaseForPlatform keeps the pre-existing behaviour everywhere the
// junction substitution does not apply. refuseAliasedRuntimeComponents still
// runs, so a symlinked owned component is refused here as before.
func acquireRuntimeLeaseForPlatform(root string) (*sandboxRuntimeLease, []windowsCreatedRuntimeDir, error) {
	if err := refuseAliasedRuntimeComponents(root); err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(filepath.Dir(root), 0o700); err != nil {
		return nil, nil, fmt.Errorf("create sandbox runtime parent: %w", err)
	}
	lease, err := acquireSandboxRuntimeLease(root)
	return lease, nil, err
}
