package sessions

import (
	"os"
	"path/filepath"
)

// leaseFileName is the file whose lock says a process has the session open.
const leaseFileName = "lease.lock"

func (store *Store) leasePath(sessionID string) string {
	return filepath.Join(store.sessionPath(sessionID), leaseFileName)
}

// Hold marks sessionID as open in this process until the process exits.
//
// A SESSION HOLDS NO LOCK BETWEEN WRITES. session.lock is taken around each
// write and released straight after, so nothing told `zero sessions prune` in
// another terminal that a TUI, an exec run or an editor still had the session
// open, and an idle open session looked exactly like an abandoned one. Hold takes
// a shared lock on the session's lease file and keeps it for the life of the
// process; prune takes the same lock exclusively and leaves alone any session it
// cannot get. Every write holds its session this way (lockSession), and so does
// every rehydrated read, which is how the TUI, `exec --resume` and ACP load a
// session in order to continue it.
//
// It never waits and never fails its caller. A lease that cannot be taken,
// because the session directory is gone or prune holds it at this moment, only
// means prune cannot see this process; the operation that asked carries on and
// meets a removed session on its own terms.
func (store *Store) Hold(sessionID string) {
	if !ValidSessionID(sessionID) {
		return
	}
	store.leasesMu.Lock()
	defer store.leasesMu.Unlock()
	if _, held := store.leases[sessionID]; held {
		return
	}
	// Creates the lease file, never the directory: a session that is gone stays
	// gone.
	file, err := openLeaseFile(store.leasePath(sessionID))
	if err != nil {
		return
	}
	if locked, err := tryLockLease(file, false); err != nil || !locked {
		_ = file.Close()
		return
	}
	if store.leases == nil {
		store.leases = map[string]*os.File{}
	}
	store.leases[sessionID] = file
}

// Release gives up this Store's lease on sessionID, for a long-lived process
// that is done with a session before it exits. A process that exits releases
// every lease with it.
func (store *Store) Release(sessionID string) {
	store.leasesMu.Lock()
	defer store.leasesMu.Unlock()
	file, held := store.leases[sessionID]
	if !held {
		return
	}
	delete(store.leases, sessionID)
	unlockLease(file)
	_ = file.Close()
}

// acquireLeaseExclusive takes the lease exclusively for as long as prune is
// removing the session, so no process can open it part way through. It reports
// false, holding nothing, when a lease is already held.
func (store *Store) acquireLeaseExclusive(sessionID string) (*os.File, bool, error) {
	file, err := openLeaseFile(store.leasePath(sessionID))
	if err != nil {
		return nil, false, err
	}
	locked, err := tryLockLease(file, true)
	if err != nil || !locked {
		_ = file.Close()
		return nil, false, err
	}
	return file, true, nil
}
