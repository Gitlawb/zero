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

func TestWindowsMountPathIsOnlySystemDrive(t *testing.T) {
	cases := []struct {
		mount, system string
		want          bool
	}{
		{`C:\`, `C:`, true},
		{`C:`, `C:`, true},
		{`c:\`, `C:`, true},
		{`D:\`, `C:`, false},
		{`C:\mnt\data`, `C:`, false},
		{`C:\mnt\data\`, `C:`, false},
		{`\\?\Volume{guid}\`, `C:`, false},
	}
	for _, tc := range cases {
		got := windowsMountPathIsOnlySystemDrive(tc.mount, tc.system)
		if got != tc.want {
			t.Fatalf("windowsMountPathIsOnlySystemDrive(%q, %q) = %v, want %v", tc.mount, tc.system, got, tc.want)
		}
	}
}

// TestWindowsMountPathsAreOnlySystemDrive pins the volume-gate fail-closed fix
// (jatmn's review): a fixed volume with no mount points at all must not be
// read as "unreachable" (it is still reachable via its raw
// "\\?\Volume{GUID}\" path), and any mount path other than the system drive
// root disqualifies the volume, matching the existing per-path behavior.
func TestWindowsMountPathsAreOnlySystemDrive(t *testing.T) {
	cases := []struct {
		name       string
		mountPaths []string
		system     string
		want       bool
	}{
		{"only system drive", []string{`C:\`}, `C:`, true},
		{"no mount points at all", nil, `C:`, false},
		{"empty mount list", []string{}, `C:`, false},
		{"extra drive letter", []string{`C:\`, `D:\`}, `C:`, false},
		{"folder mount point", []string{`C:\mnt\data`}, `C:`, false},
		{"other drive only", []string{`D:\`}, `C:`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := windowsMountPathsAreOnlySystemDrive(tc.mountPaths, tc.system)
			if got != tc.want {
				t.Fatalf("windowsMountPathsAreOnlySystemDrive(%#v, %q) = %v, want %v", tc.mountPaths, tc.system, got, tc.want)
			}
		})
	}
}
