package daemon

import (
	"fmt"
	"path/filepath"

	"github.com/Gitlawb/zero/internal/privatedir"
)

// maxUnixSocketPath bounds the socket path to the smallest platform sun_path
// limit so we surface a clear error instead of a cryptic bind failure. macOS
// allows 104 bytes incl. the NUL (so 103 chars); Linux allows 108. 103 is the
// safe cross-platform ceiling.
const maxUnixSocketPath = 103

// secureRuntimeParents creates and hardens every directory that can influence
// daemon coordination. Existing directories are migrated only after ownership
// is verified through a bound handle; directories owned by another user fail
// closed.
func secureRuntimeParents(paths Paths) error {
	parents := []struct {
		name string
		path string
	}{
		{name: "socket", path: filepath.Dir(paths.Socket)},
		{name: "lock", path: filepath.Dir(paths.Lock)},
		{name: "status", path: filepath.Dir(paths.Status)},
	}
	seen := make(map[string]struct{}, len(parents))
	for _, parent := range parents {
		absolute, err := filepath.Abs(parent.path)
		if err != nil {
			return fmt.Errorf("daemon: resolve %s directory: %w", parent.name, err)
		}
		if _, ok := seen[absolute]; ok {
			continue
		}
		seen[absolute] = struct{}{}
		if err := privatedir.Ensure(absolute); err != nil {
			return fmt.Errorf("daemon: secure %s directory: %w", parent.name, err)
		}
	}
	return nil
}

// checkSocketPathLength rejects an over-long unix socket path before bind.
func checkSocketPathLength(socketPath string) error {
	if len(socketPath) > maxUnixSocketPath {
		return fmt.Errorf("daemon: socket path too long (%d > %d): %s", len(socketPath), maxUnixSocketPath, socketPath)
	}
	return nil
}
