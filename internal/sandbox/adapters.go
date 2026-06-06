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
	Name       BackendName `json:"name"`
	Available  bool        `json:"available"`
	Executable string      `json:"executable,omitempty"`
	Message    string      `json:"message,omitempty"`
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
			return Backend{Name: BackendBubblewrap, Available: true, Executable: path, Message: "bubblewrap sandbox available"}
		}
		return Backend{Name: BackendPolicyOnly, Message: "policy-only fallback: bubblewrap is not installed"}
	case "darwin":
		if path, err := lookup("sandbox-exec"); err == nil && path != "" {
			return Backend{Name: BackendSandboxExec, Available: true, Executable: path, Message: "sandbox-exec backend available"}
		}
		return Backend{Name: BackendPolicyOnly, Message: "policy-only fallback: sandbox-exec is not available"}
	default:
		return Backend{Name: BackendPolicyOnly, Message: "policy-only fallback: no platform sandbox adapter is available for " + goos}
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
		restrictions = append(restrictions, "platform sandbox unavailable; policy engine still evaluates tool requests")
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
