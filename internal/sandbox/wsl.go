package sandbox

import "strings"

// WSLInfo describes the Windows Subsystem for Linux environment, if any. It is
// derived from /proc/version.
type WSLInfo struct {
	IsWSL  bool
	IsWSL2 bool
	Kernel string
}

// parseWSL classifies a /proc/version string. IsWSL is set when it contains
// "microsoft" or "wsl"; IsWSL2 additionally requires "microsoft" and the absence
// of the legacy "wsl1" marker. Factored out (pure) so it is table-testable
// without a real /proc on any GOOS.
func parseWSL(procVersion string) WSLInfo {
	lower := strings.ToLower(procVersion)
	isWSL := strings.Contains(lower, "microsoft") || strings.Contains(lower, "wsl")
	info := WSLInfo{IsWSL: isWSL, Kernel: strings.TrimSpace(procVersion)}
	if isWSL {
		info.IsWSL2 = strings.Contains(lower, "microsoft") && !strings.Contains(lower, "wsl1")
	}
	return info
}
