package daemon

import (
	"fmt"
	"os"
	"path/filepath"
)

// maxUnixSocketPath bounds the socket path to the smallest platform sun_path
// limit so we surface a clear error instead of a cryptic bind failure. macOS
// allows 104 bytes incl. the NUL (so 103 chars); Linux allows 108. 103 is the
// safe cross-platform ceiling.
const maxUnixSocketPath = 103

// secureSocketParent creates the socket's parent directory owner-only (0700 on
// POSIX; on Windows the per-user profile directory is already ACL-restricted).
func secureSocketParent(socketPath string) error {
	return os.MkdirAll(filepath.Dir(socketPath), 0o700)
}

// checkSocketPathLength rejects an over-long unix socket path before bind.
func checkSocketPathLength(socketPath string) error {
	if len(socketPath) > maxUnixSocketPath {
		return fmt.Errorf("daemon: socket path too long (%d > %d): %s", len(socketPath), maxUnixSocketPath, socketPath)
	}
	return nil
}
