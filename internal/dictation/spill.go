package dictation

import (
	"fmt"
	"os"
	"path/filepath"
)

// Recorded audio is staged in a hardened per-user temp directory, mirroring
// internal/tools/spill.go (PR #506): uid-suffixed name so users on a shared
// /tmp cannot collide, 0700 perms, and a pre-existing path is only accepted
// when it is a real directory (not a symlink that would redirect recordings)
// owned by the current user. Files are removed as soon as their bytes are
// read back after Stop — audio never lingers on disk.

func audioSpillRoot() string {
	name := "zero-dictation"
	if uid := os.Getuid(); uid >= 0 {
		name = fmt.Sprintf("zero-dictation-%d", uid)
	}
	return filepath.Join(os.TempDir(), name)
}

func audioSpillDir() (string, error) {
	dir := audioSpillRoot()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	// MkdirAll follows symlinks and leaves an existing directory untouched, so
	// verify what is actually at the path.
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("dictation temp path %s is not a directory", dir)
	}
	if err := checkAudioDirOwner(info); err != nil {
		return "", err
	}
	return dir, nil
}
