package sandbox

import (
	"os/exec"
	"runtime"
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
	ScopedEgress bool   `json:"scopedEgress,omitempty"`
	Executable   string `json:"executable,omitempty"`
	Message      string `json:"message,omitempty"`
}

// EnforcesScopedEgress reports whether a populated NetworkScoped allowlist can be
// enforced by this backend. When false, scoped egress must fail closed (collapse
// to deny) rather than silently run with unrestricted networking.
func (backend Backend) EnforcesScopedEgress() bool {
	return backend.Available && backend.Executable != "" && backend.ScopedEgress
}

type BackendPlan struct {
	Backend       Backend             `json:"backend"`
	WorkspaceRoot string              `json:"workspaceRoot"`
	Policy        Policy              `json:"policy"`
	SupportLevel  BackendSupportLevel `json:"supportLevel"`
	Capabilities  []BackendCapability `json:"capabilities"`
	Restrictions  []string            `json:"restrictions"`
	Warnings      []string            `json:"warnings,omitempty"`
}

type BackendCapability struct {
	Key    string           `json:"key"`
	Status CapabilityStatus `json:"status"`
	Detail string           `json:"detail"`
}

func SelectBackend(options BackendOptions) Backend {
	goos := options.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	lookup := options.LookupExecutable
	if lookup == nil {
		lookup = exec.LookPath
	}
	switch goos {
	case "linux":
		if path, err := lookup("bwrap"); err == nil && path != "" {
			return nativeBackend(goos, BackendBubblewrap, path, "bubblewrap sandbox available")
		}
		return policyOnlyBackend(goos, "policy-only fallback: bubblewrap is not installed")
	case "darwin":
		if path, err := lookup("sandbox-exec"); err == nil && path != "" {
			return nativeBackend(goos, BackendSandboxExec, path, "sandbox-exec backend available")
		}
		return policyOnlyBackend(goos, "policy-only fallback: sandbox-exec is not available")
	case "windows":
		return policyOnlyBackend(goos, "policy-only fallback: Windows native sandbox adapter is not implemented")
	default:
		return policyOnlyBackend(goos, "policy-only fallback: no platform sandbox adapter is available for "+goos)
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
		// Only sandbox-exec can enforce scoped egress today; bubblewrap's isolated
		// network namespace has no bridge to the host filtering proxy.
		ScopedEgress: name == BackendSandboxExec,
		Executable:   executable,
		Message:      message,
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
	restrictions := []string{}
	if effectivePolicy.EnforceWorkspace {
		restrictions = append(restrictions, "filesystem writes must stay inside workspace")
	}
	if effectivePolicy.Network == NetworkDeny {
		restrictions = append(restrictions, "network access denied unless a future adapter grants it explicitly")
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
	return BackendPlan{
		Backend:       backend,
		WorkspaceRoot: workspaceRoot,
		Policy:        effectivePolicy,
		SupportLevel:  backend.SupportLevel(),
		Capabilities:  backend.Capabilities(effectivePolicy),
		Restrictions:  restrictions,
		Warnings:      backend.Warnings(),
	}
}

func (backend Backend) SupportLevel() BackendSupportLevel {
	if backend.Available && backend.NativeIsolation && backend.CommandWrapping {
		return BackendSupportNative
	}
	return BackendSupportPolicyOnly
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
		warnings[0] = "Windows native sandbox adapter is not implemented; using policy-only preflight checks"
	}
	return warnings
}

func (backend Backend) Capabilities(policy Policy) []BackendCapability {
	if policy.Mode == "" {
		policy = DefaultPolicy()
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
		{
			Key:    "network_guard",
			Status: policyCapabilityStatus(policy.Mode, policy.Network == NetworkDeny),
			Detail: "network-capable tool requests are denied before execution",
		},
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
		nativeIsolation.Detail = "Windows native sandbox adapter is not implemented yet"
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
