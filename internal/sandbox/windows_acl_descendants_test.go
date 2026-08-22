package sandbox

import "testing"

func TestWindowsDescendantScanNamePolicies(t *testing.T) {
	for _, name := range []string{
		"System Volume Information",
		"SYSTEM VOLUME INFORMATION",
		"$Recycle.Bin",
		"Recovery",
	} {
		if !windowsDescendantScanNameIsSystemLocked(name) {
			t.Fatalf("windowsDescendantScanNameIsSystemLocked(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"ProgramData", "plain", "Users"} {
		if windowsDescendantScanNameIsSystemLocked(name) {
			t.Fatalf("windowsDescendantScanNameIsSystemLocked(%q) = true, want false", name)
		}
	}
}

// TestWindowsPathIsDriveRootPath pins the canonical-root-level scoping fix
// (jatmn's review): the system-locked basename allowlist must only fire
// directly under a genuine drive letter root, never at an arbitrary nested
// path that merely shares the same parent-relative shape.
func TestWindowsPathIsDriveRootPath(t *testing.T) {
	for _, path := range []string{`C:\`, `C:`, `c:\`, `Z:\`} {
		if !windowsPathIsDriveRootPath(path) {
			t.Fatalf("windowsPathIsDriveRootPath(%q) = false, want true", path)
		}
	}
	for _, path := range []string{
		`C:\ProgramData`,
		`C:\Users\Public`,
		`C:\Windows\Temp`,
		``,
		`\\?\Volume{guid}\`,
		`relative`,
	} {
		if windowsPathIsDriveRootPath(path) {
			t.Fatalf("windowsPathIsDriveRootPath(%q) = true, want false", path)
		}
	}
}
