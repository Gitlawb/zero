package sandbox

// LegacySandboxEntrypoint records sandbox code paths that should be replaced or
// reduced to thin adapters as the sandbox manager takes over.
type LegacySandboxEntrypoint struct {
	Area         string `json:"area"`
	Path         string `json:"path"`
	Symbol       string `json:"symbol"`
	DeleteTarget bool   `json:"deleteTarget"`
	Replacement  string `json:"replacement"`
}

// LegacySandboxEntrypoints is the Phase 0 inventory for the sandbox rewrite.
func LegacySandboxEntrypoints() []LegacySandboxEntrypoint {
	return []LegacySandboxEntrypoint{
		{
			Area:         "backend-selection",
			Path:         "internal/sandbox/adapters.go",
			Symbol:       "SelectBackend",
			DeleteTarget: true,
			Replacement:  "sandbox manager platform selection",
		},
		{
			Area:         "command-planning",
			Path:         "internal/sandbox/runner.go",
			Symbol:       "Engine.BuildCommandPlan",
			DeleteTarget: true,
			Replacement:  "sandbox manager command transformation",
		},
		{
			Area:         "linux-helper",
			Path:         "cmd/zero-seccomp/main.go",
			Symbol:       "zero-seccomp",
			DeleteTarget: true,
			Replacement:  "zero-linux-sandbox helper",
		},
		{
			Area:         "linux-helper",
			Path:         "internal/sandbox/seccomp_linux.go",
			Symbol:       "ApplyUnixSocketBlock",
			DeleteTarget: true,
			Replacement:  "Linux helper inner-stage seccomp setup",
		},
		{
			Area:         "wsl-fallback",
			Path:         "internal/sandbox/wsl.go",
			Symbol:       "WSLInfo",
			DeleteTarget: true,
			Replacement:  "explicit degraded policy-only platform result",
		},
		{
			Area:         "policy-json",
			Path:         "internal/cli/sandbox.go",
			Symbol:       "runSandboxPolicy",
			DeleteTarget: false,
			Replacement:  "manager-backed sandbox policy output",
		},
	}
}
