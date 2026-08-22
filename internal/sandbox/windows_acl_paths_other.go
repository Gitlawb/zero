//go:build !windows

package sandbox

import "os"

// resolveWindowsSharedDenyPaths mirrors resolveWindowsSharedDenyPaths from
// windows_acl_paths_windows.go using environment-variable fallbacks. The
// trusted-API resolution used on Windows cannot be exercised on other GOOS,
// but that carries none of the production risk it exists to close:
// BuildWindowsACLPlan's shared-deny-path logic only ever runs for real on
// Windows (applyWindowsACLPlan, which actually mutates a DACL, is itself
// windows-only), so on other platforms this is only reached from unit tests
// that inspect the plan's structure, not from an elevated setup process
// whose environment an attacker might control.
func resolveWindowsSharedDenyPaths() (systemDrive, systemRoot, programData, publicDir string, err error) {
	systemDrive = os.Getenv("SystemDrive")
	if systemDrive == "" {
		systemDrive = "C:"
	}
	systemRoot = os.Getenv("SystemRoot")
	if systemRoot == "" {
		systemRoot = systemDrive + `\Windows`
	}
	programData = os.Getenv("ProgramData")
	if programData == "" {
		programData = systemDrive + `\ProgramData`
	}
	publicDir = os.Getenv("PUBLIC")
	if publicDir == "" {
		publicDir = systemDrive + `\Users\Public`
	}
	return systemDrive, systemRoot, programData, publicDir, nil
}
