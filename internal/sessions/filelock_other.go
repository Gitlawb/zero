//go:build windows

package sessions

// acquireFileLock is a no-op fallback on platforms without flock; the in-memory
// per-Store mutex still serializes mutations within a process.
func (store *Store) acquireFileLock(sessionID string) (func(), error) {
	return func() {}, nil
}
