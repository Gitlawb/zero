package sandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

var ErrWindowsNetworkEnforcementUnavailable = errors.New("Windows sandbox network enforcement is not available")

func ValidateWindowsNetworkPolicy(network NetworkPolicy) error {
	switch network.Mode {
	case NetworkAllow:
		return nil
	case NetworkDeny, NetworkScoped, "":
		return fmt.Errorf("%w for mode %q", ErrWindowsNetworkEnforcementUnavailable, network.Mode)
	default:
		return fmt.Errorf("unsupported Windows sandbox network mode %q", network.Mode)
	}
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
