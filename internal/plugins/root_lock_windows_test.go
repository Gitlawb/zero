//go:build windows

package plugins

import (
	"path/filepath"
	"testing"
)

func TestRemoveRecoveredPluginRootLockTreatsMissingLockAsRecovered(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), pluginRootLockFileName)
	if !removeRecoveredPluginRootLock(lockPath) {
		t.Fatal("missing lock should be treated as recovered")
	}
}
