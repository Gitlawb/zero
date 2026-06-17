package sandbox

import (
	"errors"
	"fmt"
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
