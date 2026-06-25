package sandbox

import (
	"fmt"
	"strings"
)

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
	case SideEffectRead, SideEffectWrite, SideEffectShell, SideEffectNetwork, SideEffectLocalControl, SideEffectLocalBrowser, SideEffectLocalDesktop, SideEffectLocalTerminal, SideEffectOutOfWorkspace, SideEffectNone:
		return normalized
	default:
		return SideEffectOutOfWorkspace
	}
}

func NormalizeNetworkMode(value NetworkMode) NetworkMode {
	normalized := NetworkMode(strings.ToLower(strings.TrimSpace(string(value))))
	if normalized == NetworkAllow {
		return NetworkAllow
	}
	return NetworkDeny
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
