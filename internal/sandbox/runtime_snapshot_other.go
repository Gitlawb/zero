//go:build !windows

package sandbox

import "os"

// snapshotRuntimeStampBound keeps the pathname form off Windows. The split it
// closes there is specific to an elevated installer racing an unelevated
// renamer; the same eager identity capture still applies.
func snapshotRuntimeStampBound(root string) (identity string, identified bool, prior []byte, existed bool) {
	identity, identified = runtimeDirIdentity(root)
	data, err := os.ReadFile(windowsSandboxRuntimeStampPath(root))
	if err != nil {
		return identity, identified, nil, false
	}
	return identity, identified, data, true
}
