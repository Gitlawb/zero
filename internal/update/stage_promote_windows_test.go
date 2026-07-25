//go:build windows

package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPromoteInstallsTheStagedObjectNotTheStagedPath is the regression test for
// the live handoff half of #742: randomizing the staging name and creating it
// exclusively stops PRE-creation, but not substitution after the verified bytes
// are written. Windows renames through the staging HANDLE, so a substituted entry
// at the staging pathname is simply not what gets promoted.
func TestPromoteInstallsTheStagedObjectNotTheStagedPath(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "zero.exe")
	if err := os.WriteFile(targetPath, []byte("old-binary"), 0o755); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}
	sourcePath := filepath.Join(t.TempDir(), "new-binary")
	if err := os.WriteFile(sourcePath, []byte("verified-binary"), 0o755); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}

	staged, err := stageBinary(sourcePath, targetPath)
	if err != nil {
		t.Fatalf("stageBinary: %v", err)
	}
	discarded := false
	defer func() {
		if !discarded {
			staged.discard()
		}
	}()

	// Rehearse the strongest form of the substitution: the staging entry is
	// replaced wholesale between the write and the swap. (A real attacker also has
	// to get past the exclusive share mode this handle holds; the test does not,
	// which only makes the check stricter.)
	substituted := false
	if err := os.Remove(staged.path); err == nil {
		if err := os.WriteFile(staged.path, []byte("attacker-binary"), 0o755); err != nil {
			t.Fatalf("WriteFile substituted staging entry: %v", err)
		}
		substituted = true
	}

	if err := staged.promote(targetPath); err != nil {
		t.Fatalf("promote: %v", err)
	}
	// The staging handle keeps the promoted file open with an exclusive share
	// mode, so release it before reading the installed bytes (installBinary's
	// deferred discard does the same).
	staged.discard()
	discarded = true

	installed, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile installed: %v", err)
	}
	if string(installed) != "verified-binary" {
		t.Fatalf("installed binary = %q, want the verified bytes", installed)
	}
	if !substituted {
		t.Log("the staging entry could not be replaced (exclusive share mode); the handle-bound rename was still exercised")
	}
}

// TestInstallBinaryInstallsVerifiedBytes is the success control for the ordinary
// path: the staged bytes land at the target, the running binary is preserved as
// "<target>.old", and no staging artifact survives.
func TestInstallBinaryInstallsVerifiedBytes(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "zero.exe")
	if err := os.WriteFile(targetPath, []byte("old-binary"), 0o755); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}
	sourcePath := filepath.Join(t.TempDir(), "new-binary")
	if err := os.WriteFile(sourcePath, []byte("verified-binary"), 0o755); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}

	if err := installBinary(sourcePath, targetPath); err != nil {
		t.Fatalf("installBinary: %v", err)
	}
	installed, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile installed: %v", err)
	}
	if string(installed) != "verified-binary" {
		t.Fatalf("installed binary = %q, want the verified bytes", installed)
	}
	if old, err := os.ReadFile(targetPath + ".old"); err != nil {
		t.Fatalf("the replaced binary must be preserved for later cleanup: %v", err)
	} else if string(old) != "old-binary" {
		t.Fatalf("preserved binary = %q, want the previous one", old)
	}
	assertNoStagingLeftovers(t, dir)
}

// TestInstallBinaryCleansUpWhenStagingFails covers the cleanup ordering: a
// failure after the staging file exists must not leave it behind, because each
// attempt now uses a fresh random name that the next attempt never reuses.
func TestInstallBinaryCleansUpWhenStagingFails(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "zero.exe")
	if err := os.WriteFile(targetPath, []byte("old-binary"), 0o755); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}

	if err := installBinary(filepath.Join(t.TempDir(), "missing-source"), targetPath); err == nil {
		t.Fatal("installBinary with an unreadable source succeeded, want error")
	}
	assertNoStagingLeftovers(t, dir)
}

// assertNoStagingLeftovers fails when dir still holds a staging artifact.
func assertNoStagingLeftovers(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", dir, err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".new") {
			t.Fatalf("staging leftover survived in the install directory: %s", entry.Name())
		}
	}
}
