package sandbox

import (
	"fmt"
	"strings"
)

var autonomyRank = map[Autonomy]int{
	AutonomyLow:    0,
	AutonomyMedium: 1,
	AutonomyHigh:   2,
}

func NormalizeAutonomy(value Autonomy) (Autonomy, error) {
	normalized := Autonomy(strings.ToLower(strings.TrimSpace(string(value))))
	switch normalized {
	case "", AutonomyLow:
		return AutonomyLow, nil
	case AutonomyMedium, AutonomyHigh:
		return normalized, nil
	default:
		return "", fmt.Errorf("invalid sandbox autonomy %q. Expected low, medium, or high", value)
	}
}

func NormalizePermissionMode(value PermissionMode) PermissionMode {
	normalized := PermissionMode(strings.ToLower(strings.TrimSpace(string(value))))
	switch normalized {
	case PermissionModeAsk:
		return PermissionModeAsk
	case PermissionUnsafe:
		return PermissionUnsafe
	default:
		return PermissionModeAuto
	}
}

func NormalizePermission(value Permission) Permission {
	normalized := Permission(strings.ToLower(strings.TrimSpace(string(value))))
	switch normalized {
	case PermissionAllow, PermissionDeny:
		return normalized
	default:
		return PermissionPrompt
	}
}

func NormalizeSideEffect(value SideEffect) SideEffect {
	normalized := SideEffect(strings.ToLower(strings.TrimSpace(string(value))))
	switch normalized {
	case SideEffectRead, SideEffectWrite, SideEffectShell, SideEffectNetwork, SideEffectOutOfWorkspace:
		return normalized
	default:
		return SideEffectOutOfWorkspace
	}
}

func NormalizeGrantDecision(value GrantDecision) (GrantDecision, error) {
	normalized := GrantDecision(strings.ToLower(strings.TrimSpace(string(value))))
	switch normalized {
	case GrantAllow, GrantDeny:
		return normalized, nil
	default:
		return "", fmt.Errorf("invalid sandbox grant decision %q. Expected allow or deny", value)
	}
}

func autonomyAllowed(requested Autonomy, max Autonomy) bool {
	return autonomyRank[requested] <= autonomyRank[max]
}
