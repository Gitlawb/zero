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

var ErrWindowsNetworkEnforcementUnavailable = errors.New("Windows sandbox network enforcement is not available")
var ErrWindowsScopedNetworkUnavailable = errors.New("Windows sandbox scoped network enforcement is not available")

const (
	windowsWFPProviderKey = "0c3ee192-413b-4029-8a9e-991ea237ee91"
	windowsWFPSubLayerKey = "3f97d220-78f1-45c9-a530-f82ac1d487e9"
)

type WindowsNetworkPlan struct {
	Mode         NetworkMode            `json:"mode"`
	ProviderKey  string                 `json:"providerKey,omitempty"`
	SubLayerKey  string                 `json:"subLayerKey,omitempty"`
	IdentitySIDs []string               `json:"identitySids,omitempty"`
	Filters      []WindowsWFPFilterSpec `json:"filters,omitempty"`
}

type WindowsWFPFilterSpec struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	Layer  string `json:"layer"`
	Action string `json:"action"`
}

func ValidateWindowsNetworkPolicy(network NetworkPolicy) error {
	switch network.Mode {
	case NetworkAllow, NetworkDeny:
		return nil
	case NetworkScoped:
		return fmt.Errorf("%w for mode %q", ErrWindowsScopedNetworkUnavailable, network.Mode)
	case "":
		return fmt.Errorf("%w: missing network mode", ErrWindowsNetworkEnforcementUnavailable)
	default:
		return fmt.Errorf("unsupported Windows sandbox network mode %q", network.Mode)
	}
}

func BuildWindowsNetworkPlan(config WindowsSandboxCommandConfig) (WindowsNetworkPlan, error) {
	network := config.PermissionProfile.Network
	switch network.Mode {
	case NetworkAllow:
		return WindowsNetworkPlan{
			Mode:        NetworkAllow,
			ProviderKey: windowsWFPProviderKey,
			SubLayerKey: windowsWFPSubLayerKey,
		}, nil
	case NetworkDeny:
		identitySIDs, err := WindowsNetworkIdentitySIDsForConfig(config)
		if err != nil {
			return WindowsNetworkPlan{}, err
		}
		if len(identitySIDs) == 0 {
			return WindowsNetworkPlan{}, errors.New("windows network enforcement requires at least one sandbox identity SID")
		}
		return WindowsNetworkPlan{
			Mode:         NetworkDeny,
			ProviderKey:  windowsWFPProviderKey,
			SubLayerKey:  windowsWFPSubLayerKey,
			IdentitySIDs: identitySIDs,
			Filters:      windowsDenyWFPFilterSpecs(),
		}, nil
	case NetworkScoped:
		return WindowsNetworkPlan{}, fmt.Errorf("%w for mode %q", ErrWindowsScopedNetworkUnavailable, network.Mode)
	case "":
		return WindowsNetworkPlan{}, fmt.Errorf("%w: missing network mode", ErrWindowsNetworkEnforcementUnavailable)
	default:
		return WindowsNetworkPlan{}, fmt.Errorf("unsupported Windows sandbox network mode %q", network.Mode)
	}
}

func WindowsNetworkIdentitySIDsForConfig(config WindowsSandboxCommandConfig) ([]string, error) {
	identitySIDs, err := WindowsCapabilitySIDsForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("resolve Windows network identity SIDs: %w", err)
	}
	return canonicalWindowsNetworkSIDs(identitySIDs), nil
}

func WindowsNetworkPlanHash(plan WindowsNetworkPlan) (string, error) {
	plan.IdentitySIDs = canonicalWindowsNetworkSIDs(plan.IdentitySIDs)
	plan.Filters = canonicalWindowsWFPFilterSpecs(plan.Filters)
	bytes, err := json.Marshal(plan)
	if err != nil {
		return "", fmt.Errorf("marshal windows network plan hash input: %w", err)
	}
	sum := sha256.Sum256(bytes)
	return hex.EncodeToString(sum[:]), nil
}

func WindowsNetworkPolicyHash(network NetworkPolicy) (string, error) {
	allowed := normalizeDomains(network.AllowedDomains)
	denied := normalizeDomains(network.DeniedDomains)
	sort.Strings(allowed)
	sort.Strings(denied)
	canonical := struct {
		Mode           NetworkMode `json:"mode"`
		AllowedDomains []string    `json:"allowedDomains,omitempty"`
		DeniedDomains  []string    `json:"deniedDomains,omitempty"`
		ProxyRequired  bool        `json:"proxyRequired,omitempty"`
	}{
		Mode:           network.Mode,
		AllowedDomains: allowed,
		DeniedDomains:  denied,
		ProxyRequired:  network.ProxyRequired,
	}
	bytes, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("marshal windows network policy hash input: %w", err)
	}
	sum := sha256.Sum256(bytes)
	return hex.EncodeToString(sum[:]), nil
}

func windowsDenyWFPFilterSpecs() []WindowsWFPFilterSpec {
	return []WindowsWFPFilterSpec{
		{
			Key:    "cd69360b-a354-4708-8c6e-c094da814081",
			Name:   "zero_wfp_block_connect_v4",
			Layer:  "ale-auth-connect-v4",
			Action: "block",
		},
		{
			Key:    "213e6ebe-8b5b-42d9-967e-2ca380ecb601",
			Name:   "zero_wfp_block_connect_v6",
			Layer:  "ale-auth-connect-v6",
			Action: "block",
		},
	}
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
