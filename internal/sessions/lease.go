package sessions

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
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
// cannot get. Every write holds its session this way (lockSession).
//
// Hold never waits and never fails its caller. A lease that cannot be taken,
// because the session directory is gone or prune holds it at this moment, only
// means prune cannot see this process, and that is enough for a write: it lands
// under session.lock, where prune checks the session again, so either prune sees
// the write and keeps the session or the write finds the session gone. A read
// changes nothing prune checks, so the reads that continue a session, and the
// creations that read a parent, use holdOrRefuse instead.
func (store *Store) Hold(sessionID string) {
	store.hold(sessionID)
}

// ErrPruning is returned, wrapped, when a process tries to continue a session,
// or to create a session under it, while zero sessions prune holds it.
var ErrPruning = errors.New("locked by zero sessions prune; try again")

// holdOrRefuse holds a session that is about to be read in order to continue it
// (the rehydrated read behind the TUI's resume, exec --resume and --fork, and
// ACP's session/load and session/resume) or to create a session under it (Fork,
// CreateChild). When prune holds it at this moment the caller is refused rather
// than left to read it without the lease: prune could then remove a session
// this process goes on to use, or one a new session is created under, whose
// Lineage and Tree fail on a missing ancestor.
func (store *Store) holdOrRefuse(sessionID string) error {
	if store.hold(sessionID) {
		return fmt.Errorf("zero session %s is %w", sessionID, ErrPruning)
	}
	return nil
}

// hold is Hold, reporting busy when the lease could not be taken because prune
// holds it exclusively right now.
func (store *Store) hold(sessionID string) (busy bool) {
	if !ValidSessionID(sessionID) {
		return false
	}
	store.leasesMu.Lock()
	defer store.leasesMu.Unlock()
	if _, held := store.leases[sessionID]; held {
		return false
	}
	// Creates the lease file, never the directory: a session that is gone stays
	// gone.
	file, err := openLeaseFile(store.leasePath(sessionID))
	if err != nil {
		return false
	}
	locked, err := tryLockLease(file, false)
	if err != nil || !locked {
		_ = file.Close()
		return err == nil
	}
	if store.leases == nil {
		store.leases = map[string]*os.File{}
	}
	store.leases[sessionID] = file
	return false
}

// Release gives up this Store's lease on sessionID. Nothing in Zero calls it
// yet: a process holds every session it has touched until it exits, which
// releases every lease with it, and tests use Release to stand in for that
// exit. A long-lived process that switches sessions, like the TUI on /new, could
// call it for the session it leaves.
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

// HoldExclusive takes sessionID's lease exclusively without waiting, which is how
// prune keeps every other process out of a session while it checks or removes
// it. It reports false, holding nothing, when any process holds the lease. While
// it is held, Hold takes nothing and holdOrRefuse refuses. release lets it go,
// and does nothing after the first call.
func (store *Store) HoldExclusive(sessionID string) (release func(), locked bool, err error) {
	nothing := func() {}
	if !ValidSessionID(sessionID) {
		return nothing, false, fmt.Errorf("invalid zero session id %q", sessionID)
	}
	file, err := openLeaseFile(store.leasePath(sessionID))
	if err != nil {
		return nothing, false, err
	}
	locked, err = tryLockLease(file, true)
	if err != nil || !locked {
		_ = file.Close()
		return nothing, false, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			unlockLease(file)
			_ = file.Close()
		})
	}, true, nil
}
