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

func TestWindowsNetworkPolicyHashNormalizesDomainOrder(t *testing.T) {
	left, err := WindowsNetworkPolicyHash(NetworkPolicy{
		Mode:           NetworkScoped,
		AllowedDomains: []string{"API.Example.com", "example.com"},
		DeniedDomains:  []string{"blocked.example.com"},
		ProxyRequired:  true,
	})
	if err != nil {
		t.Fatalf("WindowsNetworkPolicyHash left: %v", err)
	}
	right, err := WindowsNetworkPolicyHash(NetworkPolicy{
		Mode:           NetworkScoped,
		AllowedDomains: []string{"example.com", "api.example.com"},
		DeniedDomains:  []string{"blocked.example.com"},
		ProxyRequired:  true,
	})
	if err != nil {
		t.Fatalf("WindowsNetworkPolicyHash right: %v", err)
	}
	if left != right {
		t.Fatalf("network hashes differ: %q vs %q", left, right)
	}
}
