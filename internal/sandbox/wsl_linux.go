//go:build linux

package sandbox

import "os"

// detectWSL reads /proc/version once and classifies the WSL environment. A read
// error (no /proc) yields the zero WSLInfo (not WSL).
func detectWSL() WSLInfo {
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return WSLInfo{}
	}
	return parseWSL(string(data))
}
