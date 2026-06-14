package daemon

import (
	"os"
	"path/filepath"
	"strings"
)

// DefaultDir returns the per-user directory for the daemon's runtime files:
// $XDG_RUNTIME_DIR/zero when set (a tmpfs owned by the user on Linux), otherwise
// ~/.zero. Mirrors supervisor.js / config.js choosing a per-user runtime dir.
func DefaultDir() (string, error) {
	if rt := strings.TrimSpace(os.Getenv("XDG_RUNTIME_DIR")); rt != "" {
		return filepath.Join(rt, "zero"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".zero"), nil
}

// Paths bundles the daemon's socket, lock, and status file paths.
type Paths struct {
	Socket string
	Lock   string
	Status string
}

// DefaultPaths returns the daemon control-socket, lock, and status file paths
// under DefaultDir.
func DefaultPaths() (Paths, error) {
	dir, err := DefaultDir()
	if err != nil {
		return Paths{}, err
	}
	return Paths{
		Socket: filepath.Join(dir, "daemon.sock"),
		Lock:   filepath.Join(dir, "daemon.lock"),
		Status: filepath.Join(dir, "daemon.status"),
	}, nil
}
