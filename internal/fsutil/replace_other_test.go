//go:build !windows

package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

// On Unix the replacement primitive is rename(2), which already publishes
// atomically within one filesystem and neither creates nor consults an ACL. These
// cover both shapes so the shared helper is exercised on every platform.
func TestReplaceWithRetryPublishesOverExistingAndMissingDestinations(t *testing.T) {
	for _, tc := range []struct {
		name     string
		existing bool
	}{
		{name: "existing destination", existing: true},
		{name: "missing destination"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, ".manifest.tmp")
			dst := filepath.Join(dir, "manifest.md")
			if err := os.WriteFile(src, []byte("new"), 0o600); err != nil {
				t.Fatalf("WriteFile src: %v", err)
			}
			if tc.existing {
				if err := os.WriteFile(dst, []byte("old"), 0o600); err != nil {
					t.Fatalf("WriteFile dst: %v", err)
				}
			}

			if err := ReplaceWithRetry(src, dst, nil); err != nil {
				t.Fatalf("ReplaceWithRetry: %v", err)
			}
			data, err := os.ReadFile(dst)
			if err != nil {
				t.Fatalf("ReadFile dst: %v", err)
			}
			if string(data) != "new" {
				t.Fatalf("destination content = %q, want the replacement bytes", data)
			}
			if _, err := os.Lstat(src); !os.IsNotExist(err) {
				t.Fatalf("the replacement file should be consumed by the replace: %v", err)
			}
		})
	}
}
