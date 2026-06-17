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

func TestValidateWindowsNetworkPolicyFailsClosedForScoped(t *testing.T) {
	err := ValidateWindowsNetworkPolicy(NetworkPolicy{Mode: NetworkScoped})
	if !errors.Is(err, ErrWindowsScopedNetworkUnavailable) {
		t.Fatalf("ValidateWindowsNetworkPolicy(scoped) = %v, want scoped unavailable", err)
	}
	if !strings.Contains(err.Error(), string(NetworkScoped)) {
		t.Fatalf("ValidateWindowsNetworkPolicy(scoped) error = %q, want mode detail", err)
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

func TestBuildWindowsNetworkPlanForAllowKeepsWFPNamespaceForCleanup(t *testing.T) {
	plan, err := BuildWindowsNetworkPlan(WindowsSandboxCommandConfig{
		PermissionProfile: PermissionProfile{
			Network: NetworkPolicy{Mode: NetworkAllow},
		},
	})
	if err != nil {
		t.Fatalf("BuildWindowsNetworkPlan allow: %v", err)
	}
	if plan.Mode != NetworkAllow || plan.ProviderKey == "" || plan.SubLayerKey == "" {
		t.Fatalf("allow network plan = %#v, want WFP namespace for stale filter cleanup", plan)
	}
	if len(plan.IdentitySIDs) != 0 || len(plan.Filters) != 0 {
		t.Fatalf("allow network plan = %#v, want no identities or filters", plan)
	}
}

func TestBuildWindowsNetworkPlanForDenyUsesCapabilityIdentity(t *testing.T) {
	root := t.TempDir()
	config := WindowsSandboxCommandConfig{
		SandboxHome:    t.TempDir(),
		CommandCWD:     root,
		WorkspaceRoots: []string{root},
		PermissionProfile: PermissionProfile{
			FileSystem: FileSystemPolicy{
				Kind:       FileSystemRestricted,
				ReadRoots:  []string{root},
				WriteRoots: []WritableRoot{{Root: root}},
				AllowTemp:  true,
			},
			Network: NetworkPolicy{Mode: NetworkDeny},
		},
	}
	plan, err := BuildWindowsNetworkPlan(config)
	if err != nil {
		t.Fatalf("BuildWindowsNetworkPlan: %v", err)
	}
	if plan.Mode != NetworkDeny || plan.ProviderKey == "" || plan.SubLayerKey == "" {
		t.Fatalf("network plan metadata = %#v, want deny WFP provider/sublayer", plan)
	}
	if len(plan.IdentitySIDs) != 1 || !strings.HasPrefix(plan.IdentitySIDs[0], "S-1-5-21-") {
		t.Fatalf("network identity SIDs = %#v, want generated capability SID", plan.IdentitySIDs)
	}
	if len(plan.Filters) != 2 {
		t.Fatalf("network filters = %#v, want v4/v6 connect filters", plan.Filters)
	}
}

func TestBuildWindowsNetworkPlanFailsClosedForScoped(t *testing.T) {
	root := t.TempDir()
	config := WindowsSandboxCommandConfig{
		SandboxHome:    t.TempDir(),
		CommandCWD:     root,
		WorkspaceRoots: []string{root},
		PermissionProfile: PermissionProfile{
			FileSystem: FileSystemPolicy{
				Kind:       FileSystemRestricted,
				ReadRoots:  []string{root},
				WriteRoots: []WritableRoot{{Root: root}},
			},
			Network: NetworkPolicy{Mode: NetworkScoped, AllowedDomains: []string{"example.com"}, ProxyRequired: true},
		},
	}
	_, err := BuildWindowsNetworkPlan(config)
	if !errors.Is(err, ErrWindowsScopedNetworkUnavailable) {
		t.Fatalf("BuildWindowsNetworkPlan scoped = %v, want scoped unavailable", err)
	}
}

func TestWindowsNetworkPlanHashIsStableAcrossEntryOrder(t *testing.T) {
	left, err := WindowsNetworkPlanHash(WindowsNetworkPlan{
		Mode:         NetworkDeny,
		ProviderKey:  windowsWFPProviderKey,
		SubLayerKey:  windowsWFPSubLayerKey,
		IdentitySIDs: []string{"S-1-5-21-2", "S-1-5-21-1"},
		Filters: []WindowsWFPFilterSpec{
			{Key: "b", Name: "second", Layer: "ale-auth-connect-v6", Action: "block"},
			{Key: "a", Name: "first", Layer: "ale-auth-connect-v4", Action: "block"},
		},
	})
	if err != nil {
		t.Fatalf("WindowsNetworkPlanHash left: %v", err)
	}
	right, err := WindowsNetworkPlanHash(WindowsNetworkPlan{
		Mode:         NetworkDeny,
		ProviderKey:  windowsWFPProviderKey,
		SubLayerKey:  windowsWFPSubLayerKey,
		IdentitySIDs: []string{"S-1-5-21-1", "S-1-5-21-2"},
		Filters: []WindowsWFPFilterSpec{
			{Key: "a", Name: "first", Layer: "ale-auth-connect-v4", Action: "block"},
			{Key: "b", Name: "second", Layer: "ale-auth-connect-v6", Action: "block"},
		},
	})
	if err != nil {
		t.Fatalf("WindowsNetworkPlanHash right: %v", err)
	}
	if left != right {
		t.Fatalf("network plan hashes differ: %q vs %q", left, right)
	}
}
