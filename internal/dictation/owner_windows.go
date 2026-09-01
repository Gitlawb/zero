//go:build windows

package dictation

import "os"

// checkAudioDirOwner is a no-op on Windows: %TEMP% is per-user by default and
// there is no portable uid to compare. The Lstat symlink/dir check in
// audioSpillDir still applies. Mirrors internal/tools/spill_owner_windows.go.
func checkAudioDirOwner(os.FileInfo) error {
	return nil
}
