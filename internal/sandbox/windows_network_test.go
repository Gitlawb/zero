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

func TestWindowsDenyWFPFilterSpecsMatchReferenceShape(t *testing.T) {
	specs := windowsDenyWFPFilterSpecs()
	if len(specs) != 12 {
		t.Fatalf("windowsDenyWFPFilterSpecs() len = %d, want 12", len(specs))
	}

	byName := make(map[string]WindowsWFPFilterSpec, len(specs))
	for _, spec := range specs {
		if strings.Contains(spec.Name, "block_connect") {
			t.Fatalf("filter %q is the old broad connect blocker, want targeted filters", spec.Name)
		}
		if len(spec.Conditions) != 2 {
			t.Fatalf("filter %q conditions = %#v, want user plus one network condition", spec.Name, spec.Conditions)
		}
		if spec.Conditions[0] != windowsWFPUserConditionSpec() {
			t.Fatalf("filter %q first condition = %#v, want user", spec.Name, spec.Conditions[0])
		}
		byName[spec.Name] = spec
	}

	assertWindowsWFPFilter(t, byName, "codex_wfp_icmp_connect_v4", "9f5f3812-79f0-4fe9-9615-4c2c92d2f0ff", "Block sandbox-account ICMP connect v4", "ale-auth-connect-v4", windowsWFPProtocolConditionSpec(1))
	assertWindowsWFPFilter(t, byName, "codex_wfp_icmp_connect_v6", "87498484-45ab-4510-845e-ece8b791b3bc", "Block sandbox-account ICMP connect v6", "ale-auth-connect-v6", windowsWFPProtocolConditionSpec(58))
	assertWindowsWFPFilter(t, byName, "codex_wfp_icmp_assign_v4", "af4751de-f874-4a7b-a34d-f0d0f22d1d9b", "Block sandbox-account ICMP resource assignment v4", "ale-resource-assignment-v4", windowsWFPProtocolConditionSpec(1))
	assertWindowsWFPFilter(t, byName, "codex_wfp_icmp_assign_v6", "ea10db66-a928-4b2e-a82e-a376a54f93ba", "Block sandbox-account ICMP resource assignment v6", "ale-resource-assignment-v6", windowsWFPProtocolConditionSpec(58))
	assertWindowsWFPFilter(t, byName, "codex_wfp_dns_53_v4", "83172805-f6be-4ae1-9dc6-6847aef04e7f", "Block sandbox-account DNS TCP or UDP port 53 v4", "ale-auth-connect-v4", windowsWFPRemotePortConditionSpec(53))
	assertWindowsWFPFilter(t, byName, "codex_wfp_dns_53_v6", "d23b2efb-1efb-46b2-96f3-b0ccda5690c8", "Block sandbox-account DNS TCP or UDP port 53 v6", "ale-auth-connect-v6", windowsWFPRemotePortConditionSpec(53))
	assertWindowsWFPFilter(t, byName, "codex_wfp_dns_853_v4", "420b026f-9dc9-4aea-88f4-0f2b9feab39a", "Block sandbox-account DNS-over-TLS port 853 v4", "ale-auth-connect-v4", windowsWFPRemotePortConditionSpec(853))
	assertWindowsWFPFilter(t, byName, "codex_wfp_dns_853_v6", "8d917c81-99cc-45e7-84d6-824df860cfb8", "Block sandbox-account DNS-over-TLS port 853 v6", "ale-auth-connect-v6", windowsWFPRemotePortConditionSpec(853))
	assertWindowsWFPFilter(t, byName, "codex_wfp_smb_445_v4", "e1d6e0af-ce5f-471b-b2d3-15ca00e966f3", "Block sandbox-account SMB port 445 v4", "ale-auth-connect-v4", windowsWFPRemotePortConditionSpec(445))
	assertWindowsWFPFilter(t, byName, "codex_wfp_smb_445_v6", "c2bceca4-66ef-4a0f-ba80-f4f761b8c6f0", "Block sandbox-account SMB port 445 v6", "ale-auth-connect-v6", windowsWFPRemotePortConditionSpec(445))
	assertWindowsWFPFilter(t, byName, "codex_wfp_smb_139_v4", "ba10c618-84e7-4b83-8f74-36e22b2fa1ff", "Block sandbox-account SMB port 139 v4", "ale-auth-connect-v4", windowsWFPRemotePortConditionSpec(139))
	assertWindowsWFPFilter(t, byName, "codex_wfp_smb_139_v6", "fe7f22b8-5cf5-4adb-b2aa-71fc0a8f5d44", "Block sandbox-account SMB port 139 v6", "ale-auth-connect-v6", windowsWFPRemotePortConditionSpec(139))
}

func TestWindowsDenyWFPFilterCleanupIncludesLegacyBroadConnectFilters(t *testing.T) {
	specs := windowsDenyWFPFilterSpecsToDelete()
	names := make(map[string]bool, len(specs))
	for _, spec := range specs {
		names[spec.Name] = true
	}
	for _, name := range []string{"zero_wfp_block_connect_v4", "zero_wfp_block_connect_v6"} {
		if !names[name] {
			t.Fatalf("cleanup specs missing legacy filter %q: %#v", name, specs)
		}
	}
}

func assertWindowsWFPFilter(t *testing.T, specs map[string]WindowsWFPFilterSpec, name string, key string, description string, layer string, condition WindowsWFPConditionSpec) {
	t.Helper()
	spec, ok := specs[name]
	if !ok {
		t.Fatalf("missing filter %q", name)
	}
	if spec.Key != key {
		t.Fatalf("filter %q key = %q, want %q", name, spec.Key, key)
	}
	if spec.Description != description {
		t.Fatalf("filter %q description = %q, want %q", name, spec.Description, description)
	}
	if spec.Layer != layer {
		t.Fatalf("filter %q layer = %q, want %q", name, spec.Layer, layer)
	}
	if len(spec.Conditions) != 2 || spec.Conditions[1] != condition {
		t.Fatalf("filter %q conditions = %#v, want second condition %#v", name, spec.Conditions, condition)
	}
}

// Coverage for the network infra plan + hash and the per-mode token-SID
// composition lives in windows_online_offline_test.go.
