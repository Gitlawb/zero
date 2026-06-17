package sandbox

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateWindowsNetworkPolicyAllowsNetworkAllow(t *testing.T) {
	if err := ValidateWindowsNetworkPolicy(NetworkPolicy{Mode: NetworkAllow}); err != nil {
		t.Fatalf("ValidateWindowsNetworkPolicy allow: %v", err)
	}
}

func TestValidateWindowsNetworkPolicyFailsClosedForDenyAndScoped(t *testing.T) {
	for _, mode := range []NetworkMode{NetworkDeny, NetworkScoped, ""} {
		t.Run(string(mode), func(t *testing.T) {
			err := ValidateWindowsNetworkPolicy(NetworkPolicy{Mode: mode})
			if !errors.Is(err, ErrWindowsNetworkEnforcementUnavailable) {
				t.Fatalf("ValidateWindowsNetworkPolicy(%q) = %v, want unavailable", mode, err)
			}
			if !strings.Contains(err.Error(), string(mode)) {
				t.Fatalf("ValidateWindowsNetworkPolicy(%q) error = %q, want mode detail", mode, err)
			}
		})
	}
}
