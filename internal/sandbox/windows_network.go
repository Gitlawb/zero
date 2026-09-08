package sandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

var ErrWindowsNetworkEnforcementUnavailable = errors.New("Windows sandbox network enforcement is not available") //nolint:staticcheck // Preserve the exported sentinel's established text.

const (
	windowsWFPProviderKey = "2e31d31c-3948-4753-9117-e5d1a6496f41"
	windowsWFPSubLayerKey = "e65054fd-4d32-4c7c-95ef-621f0cf6431a"
)

type WindowsNetworkPlan struct {
	Mode         NetworkMode            `json:"mode"`
	ProviderKey  string                 `json:"providerKey,omitempty"`
	SubLayerKey  string                 `json:"subLayerKey,omitempty"`
	IdentitySIDs []string               `json:"identitySids,omitempty"`
	Filters      []WindowsWFPFilterSpec `json:"filters,omitempty"`
}

type WindowsWFPFilterSpec struct {
	Key         string                    `json:"key"`
	Name        string                    `json:"name"`
	Description string                    `json:"description,omitempty"`
	Layer       string                    `json:"layer"`
	Action      string                    `json:"action"`
	Conditions  []WindowsWFPConditionSpec `json:"conditions,omitempty"`
}

type WindowsWFPConditionSpec struct {
	Kind  string `json:"kind"`
	Value uint16 `json:"value,omitempty"`
}

func ValidateWindowsNetworkPolicy(network NetworkPolicy) error {
	switch network.Mode {
	case NetworkAllow, NetworkDeny:
		return nil
	case "":
		return fmt.Errorf("%w: missing network mode", ErrWindowsNetworkEnforcementUnavailable)
	default:
		return fmt.Errorf("unsupported Windows sandbox network mode %q", network.Mode)
	}
}

// BuildWindowsNetworkInfraPlan returns the mode-INDEPENDENT network
// infrastructure that `zero sandbox setup` installs: the persistent outbound
// block filters scoped to the sandbox home's offline-marker SID. It is identical
// for allow and deny command configs — the per-command mode is enforced at
// runtime by whether the restricted token carries the offline-marker SID, not by
// which filters exist. This is what the setup marker fingerprints, so one setup
// validly serves both modes.
func BuildWindowsNetworkInfraPlan(config WindowsSandboxCommandConfig) (WindowsNetworkPlan, error) {
	offlineSID, err := WindowsOfflineMarkerSID(config.SandboxHome)
	if err != nil {
		return WindowsNetworkPlan{}, err
	}
	return WindowsNetworkPlan{
		Mode:         NetworkDeny,
		ProviderKey:  windowsWFPProviderKey,
		SubLayerKey:  windowsWFPSubLayerKey,
		IdentitySIDs: []string{offlineSID},
		Filters:      windowsDenyWFPFilterSpecs(),
	}, nil
}

// WindowsNetworkInfraHash fingerprints the provisioned (mode-independent) network
// infrastructure so the setup marker validates against the same setup for BOTH
// command modes. It never folds in the per-command network mode.
func WindowsNetworkInfraHash(plan WindowsNetworkPlan) (string, error) {
	canonical := struct {
		ProviderKey  string                 `json:"providerKey"`
		SubLayerKey  string                 `json:"subLayerKey"`
		IdentitySIDs []string               `json:"identitySids"`
		Filters      []WindowsWFPFilterSpec `json:"filters"`
	}{
		ProviderKey:  plan.ProviderKey,
		SubLayerKey:  plan.SubLayerKey,
		IdentitySIDs: canonicalWindowsNetworkSIDs(plan.IdentitySIDs),
		Filters:      canonicalWindowsWFPFilterSpecs(plan.Filters),
	}
	bytes, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("marshal windows network infra hash input: %w", err)
	}
	sum := sha256.Sum256(bytes)
	return hex.EncodeToString(sum[:]), nil
}

func windowsDenyWFPFilterSpecs() []WindowsWFPFilterSpec {
	return []WindowsWFPFilterSpec{
		{
			Key:         "cd69360b-a354-4708-8c6e-c094da814081",
			Name:        "zero_wfp_block_connect_v4",
			Description: "Block sandbox-account outbound connections v4",
			Layer:       "ale-auth-connect-v4",
			Action:      "block",
			Conditions:  []WindowsWFPConditionSpec{windowsWFPUserConditionSpec()},
		},
		{
			Key:         "213e6ebe-8b5b-42d9-967e-2ca380ecb601",
			Name:        "zero_wfp_block_connect_v6",
			Description: "Block sandbox-account outbound connections v6",
			Layer:       "ale-auth-connect-v6",
			Action:      "block",
			Conditions:  []WindowsWFPConditionSpec{windowsWFPUserConditionSpec()},
		},
		{
			Key:         "9f5f3812-79f0-4fe9-9615-4c2c92d2f0ff",
			Name:        "zero_wfp_icmp_connect_v4",
			Description: "Block sandbox-account ICMP connect v4",
			Layer:       "ale-auth-connect-v4",
			Action:      "block",
			Conditions:  []WindowsWFPConditionSpec{windowsWFPUserConditionSpec(), windowsWFPProtocolConditionSpec(1)},
		},
		{
			Key:         "87498484-45ab-4510-845e-ece8b791b3bc",
			Name:        "zero_wfp_icmp_connect_v6",
			Description: "Block sandbox-account ICMP connect v6",
			Layer:       "ale-auth-connect-v6",
			Action:      "block",
			Conditions:  []WindowsWFPConditionSpec{windowsWFPUserConditionSpec(), windowsWFPProtocolConditionSpec(58)},
		},
		{
			Key:         "af4751de-f874-4a7b-a34d-f0d0f22d1d9b",
			Name:        "zero_wfp_icmp_assign_v4",
			Description: "Block sandbox-account ICMP resource assignment v4",
			Layer:       "ale-resource-assignment-v4",
			Action:      "block",
			Conditions:  []WindowsWFPConditionSpec{windowsWFPUserConditionSpec(), windowsWFPProtocolConditionSpec(1)},
		},
		{
			Key:         "ea10db66-a928-4b2e-a82e-a376a54f93ba",
			Name:        "zero_wfp_icmp_assign_v6",
			Description: "Block sandbox-account ICMP resource assignment v6",
			Layer:       "ale-resource-assignment-v6",
			Action:      "block",
			Conditions:  []WindowsWFPConditionSpec{windowsWFPUserConditionSpec(), windowsWFPProtocolConditionSpec(58)},
		},
		{
			Key:         "83172805-f6be-4ae1-9dc6-6847aef04e7f",
			Name:        "zero_wfp_dns_53_v4",
			Description: "Block sandbox-account DNS TCP or UDP port 53 v4",
			Layer:       "ale-auth-connect-v4",
			Action:      "block",
			Conditions:  []WindowsWFPConditionSpec{windowsWFPUserConditionSpec(), windowsWFPRemotePortConditionSpec(53)},
		},
		{
			Key:         "d23b2efb-1efb-46b2-96f3-b0ccda5690c8",
			Name:        "zero_wfp_dns_53_v6",
			Description: "Block sandbox-account DNS TCP or UDP port 53 v6",
			Layer:       "ale-auth-connect-v6",
			Action:      "block",
			Conditions:  []WindowsWFPConditionSpec{windowsWFPUserConditionSpec(), windowsWFPRemotePortConditionSpec(53)},
		},
		{
			Key:         "420b026f-9dc9-4aea-88f4-0f2b9feab39a",
			Name:        "zero_wfp_dns_853_v4",
			Description: "Block sandbox-account DNS-over-TLS port 853 v4",
			Layer:       "ale-auth-connect-v4",
			Action:      "block",
			Conditions:  []WindowsWFPConditionSpec{windowsWFPUserConditionSpec(), windowsWFPRemotePortConditionSpec(853)},
		},
		{
			Key:         "8d917c81-99cc-45e7-84d6-824df860cfb8",
			Name:        "zero_wfp_dns_853_v6",
			Description: "Block sandbox-account DNS-over-TLS port 853 v6",
			Layer:       "ale-auth-connect-v6",
			Action:      "block",
			Conditions:  []WindowsWFPConditionSpec{windowsWFPUserConditionSpec(), windowsWFPRemotePortConditionSpec(853)},
		},
		{
			Key:         "e1d6e0af-ce5f-471b-b2d3-15ca00e966f3",
			Name:        "zero_wfp_smb_445_v4",
			Description: "Block sandbox-account SMB port 445 v4",
			Layer:       "ale-auth-connect-v4",
			Action:      "block",
			Conditions:  []WindowsWFPConditionSpec{windowsWFPUserConditionSpec(), windowsWFPRemotePortConditionSpec(445)},
		},
		{
			Key:         "c2bceca4-66ef-4a0f-ba80-f4f761b8c6f0",
			Name:        "zero_wfp_smb_445_v6",
			Description: "Block sandbox-account SMB port 445 v6",
			Layer:       "ale-auth-connect-v6",
			Action:      "block",
			Conditions:  []WindowsWFPConditionSpec{windowsWFPUserConditionSpec(), windowsWFPRemotePortConditionSpec(445)},
		},
		{
			Key:         "ba10c618-84e7-4b83-8f74-36e22b2fa1ff",
			Name:        "zero_wfp_smb_139_v4",
			Description: "Block sandbox-account SMB port 139 v4",
			Layer:       "ale-auth-connect-v4",
			Action:      "block",
			Conditions:  []WindowsWFPConditionSpec{windowsWFPUserConditionSpec(), windowsWFPRemotePortConditionSpec(139)},
		},
		{
			Key:         "fe7f22b8-5cf5-4adb-b2aa-71fc0a8f5d44",
			Name:        "zero_wfp_smb_139_v6",
			Description: "Block sandbox-account SMB port 139 v6",
			Layer:       "ale-auth-connect-v6",
			Action:      "block",
			Conditions:  []WindowsWFPConditionSpec{windowsWFPUserConditionSpec(), windowsWFPRemotePortConditionSpec(139)},
		},
	}
}

func windowsDenyWFPFilterSpecsToDelete() []WindowsWFPFilterSpec {
	return windowsDenyWFPFilterSpecs()
}

func windowsWFPUserConditionSpec() WindowsWFPConditionSpec {
	return WindowsWFPConditionSpec{Kind: "user"}
}

func windowsWFPProtocolConditionSpec(protocol uint16) WindowsWFPConditionSpec {
	return WindowsWFPConditionSpec{Kind: "protocol", Value: protocol}
}

func windowsWFPRemotePortConditionSpec(port uint16) WindowsWFPConditionSpec {
	return WindowsWFPConditionSpec{Kind: "remote-port", Value: port}
}

func canonicalWindowsNetworkSIDs(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i]) < strings.ToLower(out[j])
	})
	return out
}

func canonicalWindowsWFPFilterSpecs(filters []WindowsWFPFilterSpec) []WindowsWFPFilterSpec {
	out := make([]WindowsWFPFilterSpec, 0, len(filters))
	seen := map[string]struct{}{}
	for _, filter := range filters {
		filter.Key = strings.ToLower(strings.TrimSpace(filter.Key))
		filter.Name = strings.TrimSpace(filter.Name)
		filter.Layer = strings.TrimSpace(filter.Layer)
		filter.Action = strings.TrimSpace(filter.Action)
		filter.Conditions = canonicalWindowsWFPConditionSpecs(filter.Conditions)
		if filter.Key == "" || filter.Name == "" || filter.Layer == "" || filter.Action == "" {
			continue
		}
		if _, ok := seen[filter.Key]; ok {
			continue
		}
		seen[filter.Key] = struct{}{}
		out = append(out, filter)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Key < out[j].Key
	})
	return out
}

func canonicalWindowsWFPConditionSpecs(conditions []WindowsWFPConditionSpec) []WindowsWFPConditionSpec {
	out := make([]WindowsWFPConditionSpec, 0, len(conditions))
	for _, condition := range conditions {
		condition.Kind = strings.TrimSpace(condition.Kind)
		if condition.Kind == "" {
			continue
		}
		out = append(out, condition)
	}
	return out
}
