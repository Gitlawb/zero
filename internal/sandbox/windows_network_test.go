package sandbox

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateWindowsNetworkPolicyAllowsNativeModes(t *testing.T) {
	for _, mode := range []NetworkMode{NetworkAllow, NetworkDeny} {
		t.Run(string(mode), func(t *testing.T) {
			if err := ValidateWindowsNetworkPolicy(NetworkPolicy{Mode: mode}); err != nil {
				t.Fatalf("ValidateWindowsNetworkPolicy(%q): %v", mode, err)
			}
		})
	}
}

func TestValidateWindowsNetworkPolicyRejectsMissingMode(t *testing.T) {
	err := ValidateWindowsNetworkPolicy(NetworkPolicy{})
	if !errors.Is(err, ErrWindowsNetworkEnforcementUnavailable) {
		t.Fatalf("ValidateWindowsNetworkPolicy(empty) = %v, want enforcement unavailable", err)
	}
	if !strings.Contains(err.Error(), "missing network mode") {
		t.Fatalf("ValidateWindowsNetworkPolicy(empty) error = %q, want missing mode detail", err)
	}
}

// Coverage for the network infra plan + hash and the per-mode token-SID
// composition lives in windows_online_offline_test.go.
