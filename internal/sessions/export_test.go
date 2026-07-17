// Test seams: helpers only test code uses, kept out of the production binary.
package sessions

import "fmt"

// pruneOrphanBlobs removes blobs not referenced by any checkpoint event (e.g. after
// a rewind discards later checkpoints). Best-effort; returns count removed. It
// acquires the session lock so it cannot delete a blob that a concurrent
// CaptureToolCheckpoint has just written but not yet referenced by its event.
func (store *Store) pruneOrphanBlobs(sessionID string) (int, error) {
	if !ValidSessionID(sessionID) {
		return 0, fmt.Errorf("invalid zero session id %q", sessionID)
	}
	unlock, err := store.lockSession(sessionID)
	if err != nil {
		return 0, err
	}
	defer unlock()
	return store.pruneOrphanBlobsLocked(sessionID)
}
