//go:build linux

package sandbox

import (
	"bytes"
	"errors"
	"testing"
)

func TestLinuxSandboxInnerStageReliesOnOuterNamespaceForNetworkDeny(t *testing.T) {
	originalNetworkGuard := applyLinuxIsolatedNetworkGuardFilter
	originalUnixBlock := applyUnixSocketBlockFilter
	t.Cleanup(func() {
		applyLinuxIsolatedNetworkGuardFilter = originalNetworkGuard
		applyUnixSocketBlockFilter = originalUnixBlock
	})

	networkGuardCalls := 0
	unixBlockCalls := 0
	applyLinuxIsolatedNetworkGuardFilter = func() error {
		networkGuardCalls++
		return nil
	}
	applyUnixSocketBlockFilter = func() error {
		unixBlockCalls++
		return nil
	}

	var stderr bytes.Buffer
	code := runLinuxSandboxInnerStage(LinuxSandboxHelperConfig{
		PermissionProfile: PermissionProfile{Network: NetworkPolicy{Mode: NetworkDeny}},
		BlockUnixSockets:  true,
		Command:           []string{"definitely-not-a-real-zero-test-command"},
	}, &stderr)

	if code != 127 {
		t.Fatalf("exit code = %d, want lookup failure 127 after Unix-socket filter; stderr=%s", code, stderr.String())
	}
	if networkGuardCalls != 1 {
		t.Fatalf("isolated network guard calls = %d, want 1", networkGuardCalls)
	}
	if unixBlockCalls != 1 {
		t.Fatalf("unix socket filter calls = %d, want 1", unixBlockCalls)
	}
}

func TestLinuxSandboxInnerStageSkipsIsolatedNetworkGuardWhenNetworkAllowed(t *testing.T) {
	originalNetworkGuard := applyLinuxIsolatedNetworkGuardFilter
	t.Cleanup(func() {
		applyLinuxIsolatedNetworkGuardFilter = originalNetworkGuard
	})

	applyLinuxIsolatedNetworkGuardFilter = func() error {
		return errors.New("isolated network guard should not run")
	}

	var stderr bytes.Buffer
	code := runLinuxSandboxInnerStage(LinuxSandboxHelperConfig{
		PermissionProfile: PermissionProfile{Network: NetworkPolicy{Mode: NetworkAllow}},
		Command:           []string{"definitely-not-a-real-zero-test-command"},
	}, &stderr)

	if code != 127 {
		t.Fatalf("exit code = %d, want lookup failure 127 without isolated network guard; stderr=%s", code, stderr.String())
	}
}
