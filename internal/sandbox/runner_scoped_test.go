package sandbox

import (
	"context"
	"net"
	"strings"
	"testing"
)

// TestScopedNetworkGateInEvaluate verifies the preflight network gate: a
// populated scoped policy permits a network-risk tool (the proxy enforces the
// allowlist), while an empty-allowlist scoped policy fails closed exactly like
// NetworkDeny.
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

	populated := NewEngine(EngineOptions{WorkspaceRoot: root, Policy: scopedPolicy([]string{"github.com"}, nil)})
	if decision := populated.Evaluate(context.Background(), networkRequest); decision.Action == ActionDeny {
		t.Fatalf("populated scoped policy denied a network tool: %#v", decision)
	}

	empty := NewEngine(EngineOptions{WorkspaceRoot: root, Policy: scopedPolicy(nil, nil)})
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

// TestBubblewrapScopedPlanWiresProxy verifies a scoped bubblewrap plan keeps
// --unshare-net (no raw network) yet exports the proxy env so well-behaved
// clients route through the local filtering proxy, and that the plan carries a
// cleanup that shuts the proxy down.
func TestBubblewrapScopedPlanWiresProxy(t *testing.T) {
	root := t.TempDir()
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
		t.Fatalf("scoped bubblewrap plan dropped --unshare-net:\n%s", joined)
	}
	proxyAddr := proxySetenvValue(t, plan.Args, "HTTP_PROXY")
	if proxyAddr == "" {
		t.Fatalf("scoped bubblewrap plan missing HTTP_PROXY setenv:\n%s", joined)
	}
	for _, key := range []string{"HTTPS_PROXY", "ALL_PROXY"} {
		if got := proxySetenvValue(t, plan.Args, key); got != proxyAddr {
			t.Fatalf("%s setenv = %q, want %q", key, got, proxyAddr)
		}
	}
	if got := proxySetenvValue(t, plan.Args, "NO_PROXY"); !strings.Contains(got, "localhost") {
		t.Fatalf("NO_PROXY setenv = %q, want it to include localhost", got)
	}
	host, _, err := net.SplitHostPort(strings.TrimPrefix(proxyAddr, "http://"))
	if err != nil || host != "127.0.0.1" {
		t.Fatalf("proxy addr %q not bound to loopback: host=%q err=%v", proxyAddr, host, err)
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
		Backend:       Backend{Name: BackendSandboxExec, Available: true, Executable: "/usr/sbin/sandbox-exec"},
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
		Backend:       Backend{Name: BackendBubblewrap, Available: true, Executable: "/usr/bin/bwrap"},
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
