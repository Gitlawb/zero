//go:build !linux

package sandbox

// detectWSL is a no-op on non-Linux platforms: WSL is a Linux-under-Windows
// environment, so the host GOOS is never WSL. Keeps the package building on all
// platforms.
func detectWSL() WSLInfo { return WSLInfo{} }
