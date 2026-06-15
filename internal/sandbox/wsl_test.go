package sandbox

import (
	"errors"
	"strings"
	"testing"
)

func TestParseWSL(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		wantWSL  bool
		wantWSL2 bool
	}{
		{
			name:     "wsl2 microsoft kernel",
			input:    "Linux version 5.15.90.1-microsoft-standard-WSL2 (...)",
			wantWSL:  true,
			wantWSL2: true,
		},
		{
			name:     "wsl1 legacy marker",
			input:    "Linux version 4.4.0-19041-Microsoft (wsl1 build) ...",
			wantWSL:  true,
			wantWSL2: false,
		},
		{
			name:     "plain linux",
			input:    "Linux version 6.8.0-generic (gcc ...) #1 SMP",
			wantWSL:  false,
			wantWSL2: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseWSL(tc.input)
			if got.IsWSL != tc.wantWSL || got.IsWSL2 != tc.wantWSL2 {
				t.Fatalf("parseWSL(%q) = {IsWSL:%v IsWSL2:%v}, want {%v %v}", tc.input, got.IsWSL, got.IsWSL2, tc.wantWSL, tc.wantWSL2)
			}
			if got.IsWSL && got.Kernel == "" {
				t.Fatalf("parseWSL should record the kernel string")
			}
		})
	}
}

func wslBackendForTest() Backend {
	return Backend{Name: BackendWSL, Platform: "linux", Fallback: true, ProxyEgress: true}
}

func TestWSLPlanFailsClosedWithoutPolicyOnly(t *testing.T) {
	root := t.TempDir()
	policy := scopedPolicy([]string{"github.com"}, nil)
	policy.AllowPolicyOnlyRunner = false // explicitly refuse the degraded runner
	engine := NewEngine(EngineOptions{WorkspaceRoot: root, Policy: policy, Backend: wslBackendForTest()})

	_, err := engine.BuildCommandPlan(CommandSpec{Name: "/bin/sh", Args: []string{"-c", "pwd"}, Dir: root})
	if !errors.Is(err, errWSLPolicyOnlyDisabled) {
		t.Fatalf("WSL without AllowPolicyOnlyRunner err = %v, want errWSLPolicyOnlyDisabled", err)
	}
}

func TestWSLPolicyOnlyPlanCarriesProxyAndMarkers(t *testing.T) {
	root := t.TempDir()
	// scopedPolicy uses DefaultPolicy (AllowPolicyOnlyRunner=true) + an allowlist,
	// so the WSL fallback starts the filtering proxy and runs policy-only.
	engine := NewEngine(EngineOptions{WorkspaceRoot: root, Policy: scopedPolicy([]string{"github.com"}, nil), Backend: wslBackendForTest()})

	plan, err := engine.BuildCommandPlan(CommandSpec{Name: "/bin/sh", Args: []string{"-c", "pwd"}, Dir: root, Env: []string{}})
	if err != nil {
		t.Fatalf("BuildCommandPlan: %v", err)
	}
	defer plan.Cleanup()

	// Policy-only: the command runs directly, not OS-wrapped.
	if plan.Wrapped || plan.Name != "/bin/sh" {
		t.Fatalf("WSL plan must run the command directly, got wrapped=%v name=%q", plan.Wrapped, plan.Name)
	}
	env := strings.Join(plan.Env, "\n")
	if !strings.Contains(env, EnvSandboxed+"=1") {
		t.Fatalf("WSL plan env missing %s=1:\n%s", EnvSandboxed, env)
	}
	if !strings.Contains(env, EnvSandboxBackend+"=wsl") {
		t.Fatalf("WSL plan env missing %s=wsl:\n%s", EnvSandboxBackend, env)
	}
	if !strings.Contains(env, "HTTP_PROXY=") || !strings.Contains(env, "ALL_PROXY=") {
		t.Fatalf("WSL plan env missing proxy env (ProxyEnvWithSocks):\n%s", env)
	}
	if len(plan.Notes) == 0 || !strings.Contains(strings.Join(plan.Notes, " "), "policy-only") {
		t.Fatalf("WSL plan must record a least-privilege downgrade note, got %v", plan.Notes)
	}
}

func TestWSLPlanPreservesCallerEnv(t *testing.T) {
	root := t.TempDir()
	engine := NewEngine(EngineOptions{WorkspaceRoot: root, Policy: DefaultPolicy(), Backend: wslBackendForTest()})
	plan, err := engine.BuildCommandPlan(CommandSpec{
		Name: "/bin/sh", Args: []string{"-c", "true"}, Dir: root,
		Env: []string{"FOO=bar", "PATH=/custom/bin"},
	})
	if err != nil {
		t.Fatalf("BuildCommandPlan: %v", err)
	}
	defer plan.Cleanup()
	env := strings.Join(plan.Env, "\n")
	// The WSL direct-run inherits the caller's env (it is not OS-wrapped)...
	if !strings.Contains(env, "FOO=bar") || !strings.Contains(env, "PATH=/custom/bin") {
		t.Fatalf("WSL plan must preserve caller env, got:\n%s", env)
	}
	// ...with the sandbox markers appended (so they win over caller env).
	if !strings.Contains(env, EnvSandboxed+"=1") || !strings.Contains(env, EnvSandboxBackend+"=wsl") {
		t.Fatalf("WSL plan must append sandbox markers:\n%s", env)
	}
}

func TestWSLPolicyOnlyDenyPlanHasMarkersNoProxy(t *testing.T) {
	root := t.TempDir()
	// Default policy (NetworkDeny) → no proxy started, but the WSL markers + note
	// still apply and the plan still runs (AllowPolicyOnlyRunner default true).
	engine := NewEngine(EngineOptions{WorkspaceRoot: root, Policy: DefaultPolicy(), Backend: wslBackendForTest()})
	// Explicit empty env so the assertion below is not confused by a dev/CI
	// HTTP_PROXY inherited via os.Environ().
	plan, err := engine.BuildCommandPlan(CommandSpec{Name: "/bin/sh", Args: []string{"-c", "pwd"}, Dir: root, Env: []string{}})
	if err != nil {
		t.Fatalf("BuildCommandPlan: %v", err)
	}
	defer plan.Cleanup()
	env := strings.Join(plan.Env, "\n")
	if !strings.Contains(env, EnvSandboxBackend+"=wsl") || !strings.Contains(env, EnvSandboxed+"=1") {
		t.Fatalf("WSL deny plan must still carry sandbox markers:\n%s", env)
	}
	if len(plan.Notes) == 0 || !strings.Contains(strings.Join(plan.Notes, " "), "policy-only") {
		t.Fatalf("WSL deny plan must record a least-privilege downgrade note, got %v", plan.Notes)
	}
	if strings.Contains(env, "HTTP_PROXY=") || strings.Contains(env, "HTTPS_PROXY=") || strings.Contains(env, "ALL_PROXY=") {
		t.Fatalf("WSL deny plan must NOT export any proxy env vars (network denied):\n%s", env)
	}
}
