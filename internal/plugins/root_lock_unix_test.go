//go:build !windows

package plugins

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallIgnoresCrashLeftUnlockedRootLockFile(t *testing.T) {
	destDir := t.TempDir()
	src := writeSourcePlugin(t, filepath.Join(t.TempDir(), "src"), validManifest())
	if err := os.WriteFile(filepath.Join(destDir, pluginRootLockFileName), []byte("dead-pid\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := Install(context.Background(), InstallOptions{Source: src, Dir: destDir})
	if err != nil {
		t.Fatalf("Install with stale root lock file: %v", err)
	}
	if result.ID != "zero.demo" {
		t.Fatalf("unexpected install result: %#v", result)
	}
}
