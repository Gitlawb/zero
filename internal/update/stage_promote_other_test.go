//go:build !windows

package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPromoteRefusesASubstitutedStagingEntry is the regression test for the live
// handoff half of #742: randomizing the staging name and creating it exclusively
// stops PRE-creation, but not substitution after the verified bytes are written.
// POSIX cannot rename by descriptor, so staging happens inside a private 0700
// directory the attacker cannot write to, and promote additionally verifies the
// entry still names the object it wrote before renaming it into place.
func TestPromoteRefusesASubstitutedStagingEntry(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "zero")
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
	defer staged.discard()

	// First line of defence: the staging directory is private, so a principal who
	// can write in the installation directory cannot reach the entry at all.
	info, err := os.Stat(staged.dir)
	if err != nil {
		t.Fatalf("Stat staging directory: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("staging directory mode = %#o, want 0700", perm)
	}

	// Second line of defence: rehearse the substitution anyway (the test runs as
	// the directory's owner, so it can do what an attacker cannot) and require
	// promote to refuse instead of installing the substitute.
	substitute := filepath.Join(staged.dir, "substitute")
	if err := os.WriteFile(substitute, []byte("attacker-binary"), 0o755); err != nil {
		t.Fatalf("WriteFile substitute: %v", err)
	}
	if err := os.Rename(substitute, staged.path); err != nil {
		t.Fatalf("Rename substitute over the staging entry: %v", err)
	}

	if err := staged.promote(targetPath); err == nil {
		t.Fatal("promote installed a substituted staging entry, want a refusal")
	}
	installed, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile target: %v", err)
	}
	if string(installed) != "old-binary" {
		t.Fatalf("target = %q, want the original binary left in place", installed)
	}
}

// TestInstallBinaryInstallsVerifiedBytes is the success control: the ordinary
// path must still install the staged bytes, executable, with nothing left over.
func TestInstallBinaryInstallsVerifiedBytes(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "zero")
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
	info, err := os.Stat(targetPath)
	if err != nil {
		t.Fatalf("Stat installed: %v", err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("installed binary mode = %#o, want the executable bit set", info.Mode().Perm())
	}
	assertNoStagingLeftovers(t, dir)
}

// TestInstallBinaryCleansUpWhenStagingFails covers the cleanup ordering: a
// failure after the staging object exists must not leave it behind, because each
// attempt now uses a fresh random name that the next attempt never reuses.
func TestInstallBinaryCleansUpWhenStagingFails(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "zero")
	if err := os.WriteFile(targetPath, []byte("old-binary"), 0o755); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}

	if err := installBinary(filepath.Join(t.TempDir(), "missing-source"), targetPath); err == nil {
		t.Fatal("installBinary with an unreadable source succeeded, want error")
	}
	assertNoStagingLeftovers(t, dir)
}

// TestCleanupStaleBinaryRemovesAbandonedStagingDirectories covers the crash
// leftover path: a killed update leaves its private staging directory behind and
// nothing else reclaims it now that the name is random. A directory young enough
// to belong to a concurrent update must be left alone.
func TestCleanupStaleBinaryRemovesAbandonedStagingDirectories(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "zero")
	abandoned := filepath.Join(dir, stagingDirPrefix+"abandoned")
	inflight := filepath.Join(dir, stagingDirPrefix+"inflight")
	unrelated := filepath.Join(dir, "keep-me")
	for _, path := range []string{abandoned, inflight, unrelated} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatalf("Mkdir %s: %v", path, err)
		}
	}
	stale := time.Now().Add(-2 * stagingLeftoverMinAge)
	if err := os.Chtimes(abandoned, stale, stale); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	removeStaleStagingLeftovers(targetPath, time.Now())

	if _, err := os.Stat(abandoned); !os.IsNotExist(err) {
		t.Fatalf("abandoned staging directory survived: %v", err)
	}
	for _, path := range []string{inflight, unrelated} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s must be left alone: %v", path, err)
		}
	}
}

// assertNoStagingLeftovers fails when dir still holds a staging artifact.
func assertNoStagingLeftovers(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", dir, err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), stagingDirPrefix) || strings.HasSuffix(entry.Name(), ".new") {
			t.Fatalf("staging leftover survived in the install directory: %s", entry.Name())
		}
	}
}
