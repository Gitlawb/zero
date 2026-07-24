//go:build windows

package sandbox

import (
	"fmt"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// resolveWindowsSharedDenyPaths resolves the canonical system paths that
// BuildWindowsACLPlan protects with shared DenyWrite entries (the system
// drive root, %SystemRoot%\Temp, ProgramData, and the Public user profile).
//
// These are resolved from trusted Win32 APIs (GetSystemWindowsDirectory,
// SHGetKnownFolderPath) rather than the SystemDrive/SystemRoot/ProgramData/
// PUBLIC environment variables. Those variables are ordinary process
// environment state: anything able to influence the environment of the
// elevated `zero sandbox setup` process (which builds and applies this ACL
// plan) could spoof them to point the DenyWrite mitigation at the wrong
// paths, leaving the real system directories unprotected while the
// restricted token is still broadened with the Users and Authenticated
// Users SIDs (see createWindowsRestrictedTokenFromBase). The Win32 APIs used
// here are answered by the OS from its own configuration, not from the
// caller's environment block, so they are not spoofable the same way.
func resolveWindowsSharedDenyPaths() (systemDrive, systemRoot, programData, publicDir string, err error) {
	windowsDir, err := windows.GetSystemWindowsDirectory()
	if err != nil {
		return "", "", "", "", fmt.Errorf("resolve system windows directory: %w", err)
	}
	systemRoot = filepath.Clean(windowsDir)
	systemDrive = filepath.VolumeName(systemRoot)
	if systemDrive == "" {
		return "", "", "", "", fmt.Errorf("resolve system drive from windows directory %q", systemRoot)
	}
	if programData, err = windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0); err != nil {
		return "", "", "", "", fmt.Errorf("resolve ProgramData known folder: %w", err)
	}
	if publicDir, err = windows.KnownFolderPath(windows.FOLDERID_Public, 0); err != nil {
		return "", "", "", "", fmt.Errorf("resolve Public known folder: %w", err)
	}
	return systemDrive, systemRoot, programData, publicDir, nil
}
