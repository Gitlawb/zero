package sandbox

import (
	"fmt"
	"sync"
)

const sandboxRuntimeLeaseSuffix = ".lease"

// sandboxRuntimeLease is one holder's grip on a runtime root.
//
// createdFile says this acquisition is the one that brought the lease file into
// existence. Compensation needs it: rollback refuses a non-empty directory, and
// the lease sits beside the leaf inside a directory a failed setup has to be able
// to remove, so the file has to go with it. It may only go if it was ours, which
// is a fact only the create knows.
type sandboxRuntimeLease struct {
	handle runtimeLeaseHandle
	once   sync.Once
	// root is the runtime root this lease is for, kept so compensation can name
	// the lease file without deriving it again.
	root string
	// createdFile says THIS acquisition brought the lease file into existence.
	createdFile bool
}

// createdLeaseFile reports whether compensation may remove the lease file.
//
// Only the acquisition that created it may, and only while nothing else holds
// it. A lease that was already there belongs to whoever made it, and removing it
// would take the coordination object out from under a live command.
func (lease *sandboxRuntimeLease) createdLeaseFile() (string, bool) {
	if lease == nil || !lease.createdFile {
		return "", false
	}
	return sandboxRuntimeLeasePath(lease.root), true
}

func sandboxRuntimeLeasePath(root string) string {
	return root + sandboxRuntimeLeaseSuffix
}

func acquireSandboxRuntimeLease(root string) (*sandboxRuntimeLease, error) {
	handle, err := acquireSharedRuntimeLease(sandboxRuntimeLeasePath(root))
	if err != nil {
		return nil, fmt.Errorf("acquire sandbox runtime lease: %w", err)
	}
	return &sandboxRuntimeLease{handle: handle}, nil
}

func tryAcquireSandboxRuntimeCleanupLease(root string) (*sandboxRuntimeLease, bool, error) {
	handle, inUse, err := tryAcquireExclusiveRuntimeLease(root)
	if err != nil || inUse {
		return nil, inUse, err
	}
	return &sandboxRuntimeLease{handle: handle}, false, nil
}

func (lease *sandboxRuntimeLease) release() {
	if lease == nil {
		return
	}
	lease.once.Do(lease.handle.release)
}
