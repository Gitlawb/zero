package sandbox

import (
	"fmt"
	"sync"
)

const sandboxRuntimeLeaseSuffix = ".lease"

type sandboxRuntimeLease struct {
	handle runtimeLeaseHandle
	once   sync.Once
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
	handle, inUse, err := tryAcquireExclusiveRuntimeLease(sandboxRuntimeLeasePath(root))
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
