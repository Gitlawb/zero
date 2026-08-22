//go:build windows

package sandbox

import (
	"fmt"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// resolveWindowsSharedDenyPaths resolves the canonical system paths that
// earlier SID-broadening builds protected with shared DenyWrite entries
// (the system drive root, %SystemRoot%\Temp, ProgramData, and the Public
// user profile). SID broadening is disabled, so BuildWindowsACLPlan no
// longer stamps those denies; this resolver remains for tests and for any
// future access-time design that needs the same canonical roots.
//
// Paths are resolved from trusted Win32 APIs (GetSystemWindowsDirectory,
// SHGetKnownFolderPath) rather than SystemDrive/SystemRoot/ProgramData/
// PUBLIC environment variables, which are ordinary process environment
// state and spoofable by anything that can influence the elevated setup
// process.
func resolveWindowsSharedDenyPaths() (systemDrive, systemRoot, programData, publicDir string, err error) {
	// Prefer GetSystemWindowsDirectory: it always returns the shared system
	// Windows directory. GetWindowsDirectory can return a private per-session
	// path on Terminal Services for non-TSAWARE processes.
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
