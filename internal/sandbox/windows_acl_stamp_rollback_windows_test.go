//go:build windows

package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func daclOf(t *testing.T, path string) string {
	t.Helper()
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("read the DACL of %s: %v", path, err)
	}
	return descriptor.String()
}

// THE ACE AND ITS STAMP ARE ONE TRANSACTION.
//
// SetSecurityInfo commits before the stamp is written, and the apply returns no
// rollback closure on its error paths, so the caller's compensations have
// nothing to undo. A failed setup therefore reported failure while leaving the
// capability grant on a pre-existing runtime root: the tree stays writable by
// the restricted token and nothing on disk records that it should not be.
func TestAStampFailureRestoresTheCommittedACL(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(append([]string{base}, append(append([]string{}, windowsSandboxRuntimeOwnedNames...), "abcdef0123456789")...)...)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("create the runtime tree: %v", err)
	}
	before := daclOf(t, root)

	previous := windowsACLStampWriteHook
	windowsACLStampWriteHook = func(string) error { return errors.New("disk full") }
	t.Cleanup(func() { windowsACLStampWriteHook = previous })

	plan := WindowsACLPlan{Entries: []WindowsACLEntry{
		{Action: WindowsACLAllowWrite, Path: root, Capability: "S-1-5-32-546"},
	}}
	rollback, err := applyWindowsACLPlanWithStamp(plan, &windowsACLStampRequest{Root: root, PlanHash: "planhash"})
	if err == nil {
		if rollback != nil {
			_ = rollback()
		}
		t.Fatal("the apply reported success even though the stamp could not be written")
	}
	if rollback != nil {
		// A rollback closure here would be the other acceptable shape, but the
		// caller only receives one on success, so it must not be relied on.
		_ = rollback()
	}

	if after := daclOf(t, root); after != before {
		t.Errorf("the committed capability grant survived a failed setup:\nbefore %s\nafter  %s", before, after)
	}
	if _, err := os.Stat(filepath.Join(root, windowsSandboxRuntimeStampName)); err == nil {
		t.Error("a stamp exists even though the stamp step failed")
	}
}
