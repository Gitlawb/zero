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
	Executable      string      `json:"executable,omitempty"`
	Message         string      `json:"message,omitempty"`
}

type BackendPlan struct {
	Backend       Backend  `json:"backend"`
	WorkspaceRoot string   `json:"workspaceRoot"`
	Policy        Policy   `json:"policy"`
	Restrictions  []string `json:"restrictions"`
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
		Executable:      executable,
		Message:         message,
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
		Restrictions:  restrictions,
	}
}
