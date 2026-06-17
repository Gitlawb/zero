package sandbox

import (
	"os/exec"
	"runtime"
)

type SandboxPreference string

const (
	SandboxPreferenceAuto    SandboxPreference = "auto"
	SandboxPreferenceRequire SandboxPreference = "require"
	SandboxPreferenceForbid  SandboxPreference = "forbid"
)

type SandboxManagerOptions struct {
	GOOS             string
	LookupExecutable func(string) (string, error)
	Backend          Backend
}

type SandboxManager struct {
	goos    string
	backend Backend
}

type SandboxManagerRequest struct {
	WorkspaceRoot     string
	Command           CommandSpec
	Policy            Policy
	Scope             *Scope
	Profile           PermissionProfile
	Preference        SandboxPreference
	ValidateExecution bool
}

type SandboxExecutionRequest struct {
	Command                 CommandSpec         `json:"command"`
	WorkspaceRoot           string              `json:"workspaceRoot"`
	PermissionProfile       PermissionProfile   `json:"permissionProfile"`
	Backend                 Backend             `json:"backend"`
	TargetBackend           BackendName         `json:"targetBackend"`
	CommandWrapped          bool                `json:"commandWrapped"`
	SandboxEnvMarkers       []string            `json:"sandboxEnvMarkers,omitempty"`
	EnforcementLevel        EnforcementLevel    `json:"enforcementLevel"`
	DowngradeReason         string              `json:"downgradeReason,omitempty"`
	SupportLevel            BackendSupportLevel `json:"supportLevel"`
	RequiresPlatformSandbox bool                `json:"requiresPlatformSandbox"`
}

func NewSandboxManager(options SandboxManagerOptions) SandboxManager {
	goos := options.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	backend := options.Backend
	if backend.Name == "" {
		backend = selectPlatformBackend(goos, options.LookupExecutable)
	}
	if backend.Platform == "" {
		backend.Platform = goos
	}
	backend = inferBackendCapabilities(backend)
	return SandboxManager{goos: goos, backend: backend}
}

func (manager SandboxManager) Backend() Backend {
	return manager.backend
}

func selectPlatformBackend(goos string, lookup func(string) (string, error)) Backend {
	if lookup == nil {
		lookup = exec.LookPath
	}
	switch goos {
	case "linux":
		if helper, err := lookup(LinuxSandboxHelperName); err == nil && helper != "" {
			if _, bwrapErr := lookup("bwrap"); bwrapErr != nil {
				return policyOnlyBackend(goos, "policy-only fallback: bubblewrap is not installed")
			}
			return nativeBackend(goos, BackendLinuxBwrap, helper, "Linux sandbox helper available")
		}
		if info := detectWSL(); info.IsWSL {
			return wslBackend(goos, info)
		}
		return policyOnlyBackend(goos, "policy-only fallback: Linux sandbox helper is not available")
	case "darwin":
		if path, err := lookup("sandbox-exec"); err == nil && path != "" {
			return nativeBackend(goos, BackendMacOSSeatbelt, path, "macOS Seatbelt backend available")
		}
		return policyOnlyBackend(goos, "policy-only fallback: sandbox-exec is not available")
	case "windows":
		runner := findWindowsSandboxCommandRunner(lookup)
		setup := findWindowsSandboxSetupHelper(lookup)
		if runner != "" && setup != "" {
			return nativeBackend(goos, BackendWindowsRestrictedToken, runner, "Windows sandbox command runner and setup helper available")
		}
		if runner != "" {
			return policyOnlyBackend(goos, "policy-only fallback: Windows sandbox setup helper is not available")
		}
		return policyOnlyBackend(goos, "policy-only fallback: Windows sandbox command runner is not available")
	default:
		return policyOnlyBackend(goos, "policy-only fallback: no platform sandbox adapter is available for "+goos)
	}
}

func (manager SandboxManager) BuildExecutionRequest(request SandboxManagerRequest) (SandboxExecutionRequest, error) {
	policy := request.Policy
	if policy.Mode == "" {
		policy = DefaultPolicy()
	}
	preference := request.Preference
	if preference == "" {
		preference = SandboxPreferenceAuto
	}
	profile := request.Profile
	if permissionProfileUnset(profile) {
		profile = PermissionProfileFromPolicy(request.WorkspaceRoot, policy, request.Scope)
	}
	requiresPlatformSandbox := profile.RequiresPlatformSandbox() && preference != SandboxPreferenceForbid
	backend := manager.backend
	enforcementLevel := backend.EnforcementLevel(policy)
	if preference == SandboxPreferenceForbid || policy.Mode == ModeDisabled || !requiresPlatformSandbox {
		enforcementLevel = EnforcementDisabled
	}
	if request.ValidateExecution && preference == SandboxPreferenceRequire && backend.SupportLevel() != BackendSupportNative {
		return SandboxExecutionRequest{}, errPolicyOnlyRunnerDisabled
	}
	if request.ValidateExecution && requiresPlatformSandbox && backend.SupportLevel() != BackendSupportNative && !policy.AllowPolicyOnlyRunner {
		if backend.Name == BackendWSL {
			return SandboxExecutionRequest{}, errWSLPolicyOnlyDisabled
		}
		return SandboxExecutionRequest{}, errPolicyOnlyRunnerDisabled
	}
	targetBackend := manager.targetBackend(preference, policy, requiresPlatformSandbox)
	downgradeReason := ""
	if requiresPlatformSandbox && enforcementLevel == EnforcementDegraded {
		downgradeReason = backend.DowngradeReason(policy)
	}
	wrapped := backend.CommandWrapping && backend.Available && enforcementLevel == EnforcementNative
	markers := backend.SandboxEnvMarkers(policy)
	if !wrapped && backend.Name != BackendWSL {
		markers = nil
	}
	return SandboxExecutionRequest{
		Command:                 request.Command,
		WorkspaceRoot:           request.WorkspaceRoot,
		PermissionProfile:       profile,
		Backend:                 backend,
		TargetBackend:           targetBackend,
		CommandWrapped:          wrapped,
		SandboxEnvMarkers:       markers,
		EnforcementLevel:        enforcementLevel,
		DowngradeReason:         downgradeReason,
		SupportLevel:            backend.SupportLevel(),
		RequiresPlatformSandbox: requiresPlatformSandbox,
	}, nil
}

func (manager SandboxManager) BuildCommandPlan(request SandboxManagerRequest) (CommandPlan, error) {
	execRequest, err := manager.BuildExecutionRequest(request)
	if err != nil {
		return CommandPlan{}, err
	}
	policy := request.Policy
	if policy.Mode == "" {
		policy = DefaultPolicy()
	}
	return buildPlatformCommandPlan(execRequest, policy)
}

func (manager SandboxManager) targetBackend(preference SandboxPreference, policy Policy, requiresPlatformSandbox bool) BackendName {
	if preference == SandboxPreferenceForbid || policy.Mode == ModeDisabled || !requiresPlatformSandbox {
		return BackendNone
	}
	if manager.backend.Name == BackendWSL {
		return BackendPolicyOnly
	}
	return manager.backend.TargetBackend()
}

func (request SandboxExecutionRequest) BackendPlan(policy Policy) BackendPlan {
	return BackendPlan{
		Backend:                 request.Backend,
		TargetBackend:           request.TargetBackend,
		WorkspaceRoot:           request.WorkspaceRoot,
		Policy:                  policy,
		PermissionProfile:       request.PermissionProfile,
		CommandWrapped:          request.CommandWrapped,
		SandboxEnvMarkers:       request.SandboxEnvMarkers,
		EnforcementLevel:        request.EnforcementLevel,
		DowngradeReason:         request.DowngradeReason,
		SupportLevel:            request.SupportLevel,
		RequiresPlatformSandbox: request.RequiresPlatformSandbox,
		Capabilities:            request.Backend.Capabilities(policy),
		Restrictions:            request.Backend.restrictions(policy),
		Warnings:                request.Backend.Warnings(),
	}
}

func permissionProfileUnset(profile PermissionProfile) bool {
	return profile.FileSystem.Kind == "" && profile.Network.Mode == ""
}

func inferBackendCapabilities(backend Backend) Backend {
	if backend.Available && backend.Executable != "" {
		switch backend.Name {
		case BackendLinuxBwrap, BackendMacOSSeatbelt, BackendWindowsRestrictedToken:
			backend.CommandWrapping = true
			backend.NativeIsolation = true
		}
	}
	if backend.Name == BackendMacOSSeatbelt && backend.Available && backend.Executable != "" {
		backend.ScopedEgress = true
	}
	return backend
}
