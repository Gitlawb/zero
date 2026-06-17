package sandbox

import (
	"strings"
)

type BackendOptions struct {
	GOOS             string
	LookupExecutable func(string) (string, error)
}

type Backend struct {
	Name            BackendName `json:"name"`
	Available       bool        `json:"available"`
	Platform        string      `json:"platform,omitempty"`
	Fallback        bool        `json:"fallback"`
	CommandWrapping bool        `json:"commandWrapping"`
	NativeIsolation bool        `json:"nativeIsolation"`
	// ScopedEgress reports whether this backend can route a sandboxed process's
	// traffic through the local filtering proxy so a NetworkScoped allowlist is
	// actually enforced. Only sandbox-exec can today (it shares the host network
	// under a seatbelt profile that restricts outbound to the proxy port);
	// bubblewrap isolates the network namespace with no bridge to the host proxy,
	// so scoped egress there collapses to deny until a real relay exists.
	ScopedEgress bool `json:"scopedEgress,omitempty"`
	// ProxyEgress reports a backend that routes scoped egress through the local
	// filtering proxy on a BEST-EFFORT basis (the child must honor the proxy env)
	// rather than via OS-level network isolation. The WSL policy-only fallback uses
	// this: there is no netns/seatbelt to enforce egress, but the proxy still
	// applies the allow/deny gate to well-behaved clients.
	ProxyEgress bool   `json:"proxyEgress,omitempty"`
	Executable  string `json:"executable,omitempty"`
	Message     string `json:"message,omitempty"`
}

// EnforcesScopedEgress reports whether a populated NetworkScoped allowlist can be
// routed through the filtering proxy by this backend. When false, scoped egress
// must fail closed (collapse to deny) rather than silently run with unrestricted
// networking. A native backend needs an executable; the WSL fallback uses
// best-effort proxy egress (ProxyEgress) with no native isolation.
func (backend Backend) EnforcesScopedEgress() bool {
	if backend.ProxyEgress {
		return true
	}
	return backend.Available && backend.Executable != "" && backend.ScopedEgress
}

type BackendPlan struct {
	Backend                 Backend             `json:"backend"`
	TargetBackend           BackendName         `json:"targetBackend"`
	WorkspaceRoot           string              `json:"workspaceRoot"`
	Policy                  Policy              `json:"policy"`
	PermissionProfile       PermissionProfile   `json:"permissionProfile"`
	CommandWrapped          bool                `json:"commandWrapped"`
	SandboxEnvMarkers       []string            `json:"sandboxEnvMarkers,omitempty"`
	EnforcementLevel        EnforcementLevel    `json:"enforcementLevel"`
	DowngradeReason         string              `json:"downgradeReason,omitempty"`
	SupportLevel            BackendSupportLevel `json:"supportLevel"`
	RequiresPlatformSandbox bool                `json:"requiresPlatformSandbox"`
	Capabilities            []BackendCapability `json:"capabilities"`
	Restrictions            []string            `json:"restrictions"`
	Warnings                []string            `json:"warnings,omitempty"`
}

type BackendCapability struct {
	Key    string           `json:"key"`
	Status CapabilityStatus `json:"status"`
	Detail string           `json:"detail"`
}

func SelectBackend(options BackendOptions) Backend {
	return NewSandboxManager(SandboxManagerOptions{
		GOOS:             options.GOOS,
		LookupExecutable: options.LookupExecutable,
	}).Backend()
}

func TargetBackendForPlatform(goos string, wsl bool) BackendName {
	switch goos {
	case "darwin":
		return BackendMacOSSeatbelt
	case "linux":
		if wsl {
			return BackendPolicyOnly
		}
		return BackendLinuxBwrap
	case "windows":
		return BackendWindowsRestrictedToken
	default:
		return BackendPolicyOnly
	}
}

func (backend Backend) TargetBackend() BackendName {
	if backend.Platform == "windows" {
		if backend.Name == BackendWindowsElevated || backend.Name == BackendWindowsRestrictedToken {
			return backend.Name
		}
		return BackendWindowsRestrictedToken
	}
	switch backend.Name {
	case BackendWSL:
		return BackendPolicyOnly
	case BackendNone, BackendMacOSSeatbelt, BackendLinuxBwrap, BackendLinuxLandlock, BackendWindowsRestrictedToken, BackendWindowsElevated, BackendPolicyOnly:
		return backend.Name
	default:
		return TargetBackendForPlatform(backend.Platform, false)
	}
}

func nativeBackend(goos string, name BackendName, executable string, message string) Backend {
	return Backend{
		Name:            name,
		Available:       true,
		Platform:        goos,
		Fallback:        false,
		CommandWrapping: true,
		NativeIsolation: true,
		ScopedEgress:    name == BackendMacOSSeatbelt,
		Executable:      executable,
		Message:         message,
	}
}

// wslBackend is the policy-only WSL fallback: no native isolation, but scoped
// egress is routed best-effort through the filtering proxy (ProxyEgress). The
// runner gates it on Policy.AllowPolicyOnlyRunner and records a downgrade note.
func wslBackend(goos string, info WSLInfo) Backend {
	msg := "policy-only WSL fallback: bubblewrap unavailable under WSL; egress routed through the filtering proxy"
	if info.IsWSL2 {
		msg = "policy-only WSL2 fallback: bubblewrap unavailable/unreliable under WSL2; egress routed through the filtering proxy"
	}
	return Backend{
		Name:            BackendWSL,
		Available:       false,
		Platform:        goos,
		Fallback:        true,
		CommandWrapping: false,
		NativeIsolation: false,
		ProxyEgress:     true,
		Message:         msg,
	}
}

func policyOnlyBackend(goos string, message string) Backend {
	return Backend{
		Name:            BackendPolicyOnly,
		Available:       false,
		Platform:        goos,
		Fallback:        true,
		CommandWrapping: false,
		NativeIsolation: false,
		Message:         message,
	}
}

func (backend Backend) BuildPlan(workspaceRoot string, policy Policy) BackendPlan {
	effectivePolicy := policy
	if effectivePolicy.Mode == "" {
		effectivePolicy = DefaultPolicy()
	}
	profile := PermissionProfileFromPolicy(workspaceRoot, effectivePolicy, nil)
	execRequest, _ := NewSandboxManager(SandboxManagerOptions{
		GOOS:    backend.Platform,
		Backend: backend,
	}).BuildExecutionRequest(SandboxManagerRequest{
		WorkspaceRoot: workspaceRoot,
		Policy:        effectivePolicy,
		Profile:       profile,
		Preference:    SandboxPreferenceAuto,
	})
	return execRequest.BackendPlan(effectivePolicy)
}

func (backend Backend) restrictions(policy Policy) []string {
	effectivePolicy := policy
	if effectivePolicy.Mode == "" {
		effectivePolicy = DefaultPolicy()
	}
	restrictions := []string{}
	if effectivePolicy.EnforceWorkspace {
		restrictions = append(restrictions, "filesystem writes must stay inside workspace")
	}
	if effectivePolicy.Network == NetworkDeny {
		if backend.Name == BackendWindowsRestrictedToken && backend.NativeIsolation {
			restrictions = append(restrictions, "Windows WFP filters block outbound network for sandbox identities")
		} else {
			restrictions = append(restrictions, "network access denied unless a future adapter grants it explicitly")
		}
	}
	if effectivePolicy.DenyDestructiveShell {
		restrictions = append(restrictions, "destructive shell patterns denied before execution")
	}
	if backend.Name == BackendPolicyOnly {
		platform := backend.Platform
		if platform == "" {
			platform = "this platform"
		}
		restrictions = append(restrictions, "native process isolation unavailable on "+platform+"; policy engine still evaluates tool requests before execution")
		restrictions = append(restrictions, "shell commands are not wrapped by a native platform sandbox")
	} else if backend.Available {
		restrictions = append(restrictions, "shell commands are wrapped through "+string(backend.Name)+" when launched by the sandbox engine")
	}
	return restrictions
}

func (backend Backend) SupportLevel() BackendSupportLevel {
	if backend.Available && backend.NativeIsolation && backend.CommandWrapping {
		return BackendSupportNative
	}
	return BackendSupportPolicyOnly
}

func (backend Backend) EnforcementLevel(policy Policy) EnforcementLevel {
	if policy.Mode == "" {
		policy = DefaultPolicy()
	}
	if policy.Mode == ModeDisabled {
		return EnforcementDisabled
	}
	if backend.SupportLevel() == BackendSupportNative {
		return EnforcementNative
	}
	return EnforcementDegraded
}

func (backend Backend) DowngradeReason(policy Policy) string {
	if policy.Mode == "" {
		policy = DefaultPolicy()
	}
	if policy.Mode == ModeDisabled {
		return "sandbox policy disabled"
	}
	if backend.SupportLevel() == BackendSupportNative {
		return ""
	}
	if strings.TrimSpace(backend.Message) != "" {
		return backend.Message
	}
	platform := backend.Platform
	if platform == "" {
		platform = "this platform"
	}
	return "native sandbox unavailable on " + platform
}

func (backend Backend) SandboxEnvMarkers(policy Policy) []string {
	if policy.Mode == "" {
		policy = DefaultPolicy()
	}
	if policy.Mode == ModeDisabled {
		return nil
	}
	if !(backend.CommandWrapping && backend.Available) && backend.Name != BackendWSL {
		return nil
	}
	name := backend.Name
	if name == "" {
		name = BackendPolicyOnly
	}
	return []string{
		EnvSandboxed + "=1",
		EnvSandboxBackend + "=" + string(name),
		"ZERO_SANDBOX_NETWORK=" + string(policy.Network),
	}
}

func (backend Backend) Warnings() []string {
	if backend.SupportLevel() == BackendSupportNative {
		return nil
	}
	platform := backend.Platform
	if platform == "" {
		platform = "this platform"
	}
	warnings := []string{
		"native process isolation unavailable on " + platform,
		"shell commands are not wrapped by a native platform sandbox",
	}
	if backend.Platform == "windows" {
		warnings[0] = "Windows sandbox command runner is not available; using policy-only preflight checks"
	}
	return warnings
}

func (backend Backend) Capabilities(policy Policy) []BackendCapability {
	if policy.Mode == "" {
		policy = DefaultPolicy()
	}
	networkGuard := BackendCapability{
		Key:    "network_guard",
		Status: policyCapabilityStatus(policy.Mode, policy.Network == NetworkDeny),
		Detail: "network-capable tool requests are denied before execution",
	}
	if policy.Mode != ModeDisabled && policy.Network == NetworkDeny && backend.Name == BackendWindowsRestrictedToken && backend.NativeIsolation {
		networkGuard.Status = CapabilityNative
		networkGuard.Detail = "Windows WFP filters block outbound network for sandbox identities"
	}
	capabilities := []BackendCapability{
		{
			Key:    "policy_evaluation",
			Status: policyCapabilityStatus(policy.Mode, true),
			Detail: "tool requests are evaluated against sandbox policy before execution",
		},
		{
			Key:    "workspace_write_guard",
			Status: policyCapabilityStatus(policy.Mode, policy.EnforceWorkspace),
			Detail: "filesystem writes are checked against the workspace root before execution",
		},
		networkGuard,
		{
			Key:    "destructive_shell_guard",
			Status: policyCapabilityStatus(policy.Mode, policy.DenyDestructiveShell),
			Detail: "destructive shell patterns are denied before execution",
		},
	}
	nativeIsolation := BackendCapability{
		Key:    "native_process_isolation",
		Status: CapabilityUnavailable,
		Detail: "no native process sandbox is active for this platform",
	}
	if backend.NativeIsolation {
		nativeIsolation.Status = CapabilityNative
		nativeIsolation.Detail = "tool subprocesses can run inside " + string(backend.Name)
	} else if backend.Platform == "windows" {
		nativeIsolation.Detail = "Windows sandbox command runner is not available"
	}
	commandWrapping := BackendCapability{
		Key:    "command_wrapping",
		Status: CapabilityUnavailable,
		Detail: "shell commands are not wrapped by a native platform sandbox",
	}
	if backend.CommandWrapping {
		commandWrapping.Status = CapabilityNative
		commandWrapping.Detail = "shell commands can be launched through " + string(backend.Name)
	}
	return append(capabilities, nativeIsolation, commandWrapping)
}

func policyCapabilityStatus(mode PolicyMode, enabled bool) CapabilityStatus {
	if mode == ModeDisabled || !enabled {
		return CapabilityDisabled
	}
	return CapabilityPreflight
}
