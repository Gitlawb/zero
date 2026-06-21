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
