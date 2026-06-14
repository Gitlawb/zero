package sandbox

import (
	"context"
	"strings"
	"testing"
)

// TestScopedNetworkGateInEvaluate verifies the preflight network gate: a populated
// scoped policy permits a network-risk tool ONLY when the active backend can
// actually route through the filtering proxy. A backend that cannot enforce scoped
// egress (bubblewrap's isolated netns has no bridge; policy-only has no isolation)
// fails closed and denies, exactly like an empty-allowlist scoped policy.
func TestScopedNetworkGateInEvaluate(t *testing.T) {
	root := t.TempDir()
	networkRequest := Request{
		ToolName:       "web_fetch",
		SideEffect:     SideEffectNetwork,
		Permission:     PermissionAllow,
		PermissionMode: PermissionModeAuto,
		Autonomy:       AutonomyHigh,
		Args:           map[string]any{"url": "https://github.com"},
	}
	sandboxExec := Backend{Name: BackendSandboxExec, Available: true, Executable: "/usr/sbin/sandbox-exec", ScopedEgress: true}

	// This test exercises the network GATE for a network tool, so it opts the
	// in-process tools into the network policy (EnforceToolNetwork); by default
	// those tools are exempt and would not be gated here.
	enforcedScoped := func(allowed, denied []string) Policy {
		p := scopedPolicy(allowed, denied)
		p.EnforceToolNetwork = true
		return p
	}

	// An enforcing backend lets a populated scoped policy permit a network tool.
	enforcing := NewEngine(EngineOptions{WorkspaceRoot: root, Policy: enforcedScoped([]string{"github.com"}, nil), Backend: sandboxExec})
	if decision := enforcing.Evaluate(context.Background(), networkRequest); decision.Action == ActionDeny {
		t.Fatalf("enforcing backend + scoped policy denied a network tool: %#v", decision)
	}

	// Backends that cannot enforce scoped egress must fail closed (deny).
	unenforceable := map[string]Backend{
		"bubblewrap":  {Name: BackendBubblewrap, Available: true, Executable: "/usr/bin/bwrap"},
		"policy-only": {},
	}
	for name, backend := range unenforceable {
		engine := NewEngine(EngineOptions{WorkspaceRoot: root, Policy: enforcedScoped([]string{"github.com"}, nil), Backend: backend})
		decision := engine.Evaluate(context.Background(), networkRequest)
		if decision.Action != ActionDeny || decision.Violation == nil || decision.Violation.Code != ViolationNetwork {
			t.Fatalf("%s backend must deny scoped network it cannot enforce, got %#v", name, decision)
		}
	}

	// Empty allowlist still fails closed regardless of backend.
	empty := NewEngine(EngineOptions{WorkspaceRoot: root, Policy: enforcedScoped(nil, nil), Backend: sandboxExec})
	decision := empty.Evaluate(context.Background(), networkRequest)
	if decision.Action != ActionDeny || decision.Violation == nil || decision.Violation.Code != ViolationNetwork {
		t.Fatalf("empty scoped policy must deny network like NetworkDeny, got %#v", decision)
	}
}

// scopedPolicy is DefaultPolicy with NetworkScoped and the given allow/deny
// lists, used across the scoped-egress runner tests.
func scopedPolicy(allowed []string, denied []string) Policy {
	policy := DefaultPolicy()
	policy.Network = NetworkScoped
	policy.AllowedDomains = allowed
	policy.DeniedDomains = denied
	return policy
}

// TestEffectiveNetworkScopedEmptyIsDeny pins the fail-closed rule: NetworkScoped
// with no allowlisted domains is treated exactly like NetworkDeny.
func TestEffectiveNetworkScopedEmptyIsDeny(t *testing.T) {
	if got := effectiveNetwork(scopedPolicy(nil, nil)); got != NetworkDeny {
		t.Fatalf("effectiveNetwork(scoped, empty allowlist) = %q, want deny", got)
	}
	if got := effectiveNetwork(scopedPolicy([]string{"   "}, nil)); got != NetworkDeny {
		t.Fatalf("effectiveNetwork(scoped, blank-only allowlist) = %q, want deny", got)
	}
	if got := effectiveNetwork(scopedPolicy([]string{"github.com"}, nil)); got != NetworkScoped {
		t.Fatalf("effectiveNetwork(scoped, with allowlist) = %q, want scoped", got)
	}
	// Existing modes are unchanged.
	if got := effectiveNetwork(DefaultPolicy()); got != NetworkDeny {
		t.Fatalf("effectiveNetwork(default) = %q, want deny", got)
	}
	allow := DefaultPolicy()
	allow.Network = NetworkAllow
	if got := effectiveNetwork(allow); got != NetworkAllow {
		t.Fatalf("effectiveNetwork(allow) = %q, want allow", got)
	}
}

// TestBubblewrapScopedPlanFailsClosedWithoutProxy verifies that, because
// bubblewrap isolates the network namespace (--unshare-net) with no bridge to the
// host loopback proxy, a scoped policy collapses to deny: the plan keeps
// --unshare-net, never exports a proxy, and never starts an (unreachable) proxy.
func TestBubblewrapScopedPlanFailsClosedWithoutProxy(t *testing.T) {
	root := t.TempDir()
	started := false
	restore := startEgressProxy
	startEgressProxy = func(egressOptions) (*egressProxy, error) {
		started = true
		return nil, errEmptyAllowlist
	}
	defer func() { startEgressProxy = restore }()

	engine := NewEngine(EngineOptions{
		WorkspaceRoot: root,
		Policy:        scopedPolicy([]string{"github.com"}, nil),
		Backend:       Backend{Name: BackendBubblewrap, Available: true, Executable: "/usr/bin/bwrap"},
	})
	plan, err := engine.BuildCommandPlan(CommandSpec{Name: "/bin/sh", Args: []string{"-c", "pwd"}, Dir: root})
	if err != nil {
		t.Fatalf("BuildCommandPlan: %v", err)
	}
	defer plan.Cleanup()

	joined := strings.Join(plan.Args, " ")
	if !strings.Contains(joined, "--unshare-net") {
		t.Fatalf("scoped bubblewrap plan must keep --unshare-net (deny-equivalent):\n%s", joined)
	}
	if proxyAddr := proxySetenvValue(t, plan.Args, "HTTP_PROXY"); proxyAddr != "" {
		t.Fatalf("scoped bubblewrap plan must not export a proxy it cannot reach:\n%s", joined)
	}
	if started {
		t.Fatal("bubblewrap must not start an unreachable egress proxy for a scoped policy")
	}
}

// TestSandboxExecScopedPlanWiresProxy verifies a scoped sandbox-exec profile
// denies general network but permits localhost (the proxy port) and sets the
// proxy env.
func TestSandboxExecScopedPlanWiresProxy(t *testing.T) {
	root := t.TempDir()
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: root,
		Policy:        scopedPolicy([]string{"github.com"}, nil),
		Backend:       Backend{Name: BackendSandboxExec, Available: true, Executable: "/usr/sbin/sandbox-exec", ScopedEgress: true},
	})
	plan, err := engine.BuildCommandPlan(CommandSpec{Name: "/bin/sh", Args: []string{"-c", "pwd"}, Dir: root})
	if err != nil {
		t.Fatalf("BuildCommandPlan: %v", err)
	}
	defer plan.Cleanup()

	if len(plan.Args) < 2 || plan.Args[0] != "-p" {
		t.Fatalf("sandbox-exec args = %#v, want profile", plan.Args)
	}
	profile := plan.Args[1]
	if strings.Contains(profile, "(allow network*)") {
		t.Fatalf("scoped sandbox-exec must not allow all network:\n%s", profile)
	}
	if !strings.Contains(profile, "network-outbound") || !strings.Contains(profile, "localhost") {
		t.Fatalf("scoped sandbox-exec profile must allow localhost outbound:\n%s", profile)
	}
	var proxyAddr string
	for _, env := range plan.Env {
		if strings.HasPrefix(env, "HTTP_PROXY=") {
			proxyAddr = strings.TrimPrefix(env, "HTTP_PROXY=")
		}
	}
	if proxyAddr == "" {
		t.Fatalf("scoped sandbox-exec plan missing HTTP_PROXY env: %#v", plan.Env)
	}
}

// TestScopedEmptyAllowlistBuildsLikeDeny verifies a scoped plan with an empty
// allowlist produces a deny-equivalent plan (no proxy, no network) and never
// starts a proxy.
func TestScopedEmptyAllowlistBuildsLikeDeny(t *testing.T) {
	root := t.TempDir()
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: root,
		Policy:        scopedPolicy(nil, nil),
		Backend:       Backend{Name: BackendBubblewrap, Available: true, Executable: "/usr/bin/bwrap"},
	})
	plan, err := engine.BuildCommandPlan(CommandSpec{Name: "/bin/sh", Args: []string{"-c", "pwd"}, Dir: root})
	if err != nil {
		t.Fatalf("BuildCommandPlan: %v", err)
	}
	defer plan.Cleanup()

	joined := strings.Join(plan.Args, " ")
	if !strings.Contains(joined, "--unshare-net") {
		t.Fatalf("empty scoped plan must keep --unshare-net (deny-equivalent):\n%s", joined)
	}
	if proxySetenvValue(t, plan.Args, "HTTP_PROXY") != "" {
		t.Fatalf("empty scoped plan must not export a proxy:\n%s", joined)
	}
}

// TestScopedProxyStartFailureDeniesNetwork verifies that if the egress proxy
// cannot start, the command is denied network (an error) and never falls back to
// open network access.
func TestScopedProxyStartFailureDeniesNetwork(t *testing.T) {
	root := t.TempDir()
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: root,
		Policy:        scopedPolicy([]string{"github.com"}, nil),
		// sandbox-exec actually starts the proxy, so its failure must fail closed.
		Backend: Backend{Name: BackendSandboxExec, Available: true, Executable: "/usr/sbin/sandbox-exec", ScopedEgress: true},
	})
	// Force the proxy factory to fail; the build must surface an error rather than
	// degrade to an unproxied (open) network plan.
	restore := startEgressProxy
	startEgressProxy = func(egressOptions) (*egressProxy, error) {
		return nil, errEmptyAllowlist
	}
	defer func() { startEgressProxy = restore }()

	if _, err := engine.BuildCommandPlan(CommandSpec{Name: "/bin/sh", Args: []string{"-c", "pwd"}, Dir: root}); err == nil {
		t.Fatal("BuildCommandPlan with failing proxy = nil error, want fail-closed deny")
	}
}

func proxySetenvValue(t *testing.T, args []string, key string) string {
	t.Helper()
	for index := 0; index+2 < len(args); index++ {
		if args[index] == "--setenv" && args[index+1] == key {
			return args[index+2]
		}
	}
	return ""
}
