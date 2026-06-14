package sandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBuildCommandPlanWrapsBubblewrap(t *testing.T) {
	root := t.TempDir()
	resolvedRoot := resolvedTestPath(t, root)
	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: root,
		Policy:        DefaultPolicy(),
		Backend: Backend{
			Name:       BackendBubblewrap,
			Available:  true,
			Executable: "/usr/bin/bwrap",
			Message:    "bubblewrap sandbox available",
		},
	})

	plan, err := engine.BuildCommandPlan(CommandSpec{
		Name: "/bin/sh",
		Args: []string{"-c", "pwd"},
		Dir:  nested,
	})
	if err != nil {
		t.Fatalf("BuildCommandPlan: %v", err)
	}

	if !plan.Wrapped || plan.Name != "/usr/bin/bwrap" || plan.Backend.Name != BackendBubblewrap {
		t.Fatalf("plan backend = %#v, want wrapped bubblewrap", plan)
	}
	assertArgsContainSequence(t, plan.Args, "--bind", resolvedRoot, bubblewrapWorkspace)
	assertArgsContainSequence(t, plan.Args, "--chdir", bubblewrapWorkspace+"/nested")
	assertArgsContainSequence(t, plan.Args, "--unshare-net")
	assertArgsContainSequence(t, plan.Args, "--", "/bin/sh", "-c", "pwd")
	if plan.SandboxDir != bubblewrapWorkspace+"/nested" {
		t.Fatalf("SandboxDir = %q, want nested workspace dir", plan.SandboxDir)
	}
	if plan.Dir != "" {
		t.Fatalf("bubblewrap host Dir = %q, want empty because bwrap owns chdir", plan.Dir)
	}
}

func TestBuildCommandPlanWrapsSandboxExec(t *testing.T) {
	root := t.TempDir()
	resolvedRoot := resolvedTestPath(t, root)
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: root,
		Policy:        DefaultPolicy(),
		Backend: Backend{
			Name:       BackendSandboxExec,
			Available:  true,
			Executable: "/usr/bin/sandbox-exec",
			Message:    "sandbox-exec backend available",
		},
	})

	plan, err := engine.BuildCommandPlan(CommandSpec{
		Name: "/bin/sh",
		Args: []string{"-c", "pwd"},
		Dir:  root,
	})
	if err != nil {
		t.Fatalf("BuildCommandPlan: %v", err)
	}

	if !plan.Wrapped || plan.Name != "/usr/bin/sandbox-exec" || plan.Backend.Name != BackendSandboxExec {
		t.Fatalf("plan backend = %#v, want wrapped sandbox-exec", plan)
	}
	if len(plan.Args) < 5 || plan.Args[0] != "-p" {
		t.Fatalf("sandbox-exec args = %#v, want profile and command", plan.Args)
	}
	profile := plan.Args[1]
	for _, want := range []string{
		"(deny default)",
		"(deny network*)",
		`(subpath "` + sandboxProfileString(resolvedRoot) + `")`,
		`(literal "/dev/null")`,
		`(subpath "/private/tmp")`,
	} {
		if !strings.Contains(profile, want) {
			t.Fatalf("profile missing %q:\n%s", want, profile)
		}
	}
	assertArgsContainSequence(t, plan.Args, "/bin/sh", "-c", "pwd")
	if plan.Dir != resolvedRoot || plan.SandboxDir != resolvedRoot {
		t.Fatalf("sandbox-exec dirs = host %q sandbox %q, want %q", plan.Dir, plan.SandboxDir, resolvedRoot)
	}
}

func TestBuildCommandPlanUsesPolicyOnlyFallback(t *testing.T) {
	root := t.TempDir()
	resolvedRoot := resolvedTestPath(t, root)
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: root,
		Policy:        DefaultPolicy(),
		Backend:       Backend{Name: BackendPolicyOnly, Message: "policy-only fallback"},
	})

	plan, err := engine.BuildCommandPlan(CommandSpec{
		Name: "/bin/sh",
		Args: []string{"-c", "pwd"},
		Dir:  root,
	})
	if err != nil {
		t.Fatalf("BuildCommandPlan: %v", err)
	}

	if plan.Wrapped || plan.Name != "/bin/sh" || plan.Dir != resolvedRoot || plan.WorkspaceRoot != resolvedRoot || plan.Backend.Name != BackendPolicyOnly {
		t.Fatalf("policy-only plan = %#v, want direct command", plan)
	}
}

func TestBuildCommandPlanCanRejectPolicyOnlyFallback(t *testing.T) {
	root := t.TempDir()
	policy := DefaultPolicy()
	policy.AllowPolicyOnlyRunner = false
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: root,
		Policy:        policy,
		Backend:       Backend{Name: BackendPolicyOnly, Message: "policy-only fallback"},
	})

	_, err := engine.BuildCommandPlan(CommandSpec{Name: "/bin/sh", Dir: root})
	if !errors.Is(err, errPolicyOnlyRunnerDisabled) {
		t.Fatalf("error = %v, want policy-only disabled", err)
	}
}

func TestBuildCommandPlanRejectsOutsideDirectory(t *testing.T) {
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: t.TempDir(),
		Policy:        DefaultPolicy(),
		Backend:       Backend{Name: BackendPolicyOnly},
	})

	_, err := engine.BuildCommandPlan(CommandSpec{Name: "/bin/sh", Dir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "outside_workspace") {
		t.Fatalf("error = %v, want outside workspace violation", err)
	}
}

func assertArgsContainSequence(t *testing.T, args []string, sequence ...string) {
	t.Helper()
	if len(sequence) == 0 {
		return
	}
	for index := 0; index <= len(args)-len(sequence); index++ {
		matched := true
		for offset, want := range sequence {
			if args[index+offset] != want {
				matched = false
				break
			}
		}
		if matched {
			return
		}
	}
	t.Fatalf("args %#v do not contain sequence %#v", args, sequence)
}

// TestSandboxExecProfileAllowsDevNullAndTemp reproduces the audit finding that
// the generated sandbox-exec profile blocked `> /dev/null` and mktemp because
// only the workspace was writable. It runs real commands through sandbox-exec
// when that backend is available on the host.
func TestSandboxExecProfileAllowsDevNullAndTemp(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec is macOS-only")
	}
	backend := SelectBackend(BackendOptions{})
	if !backend.Available || backend.Name != BackendSandboxExec {
		t.Skipf("sandbox-exec backend unavailable: %s", backend.Message)
	}
	root := t.TempDir()
	engine := NewEngine(EngineOptions{WorkspaceRoot: root, Policy: DefaultPolicy(), Backend: backend})

	run := func(script string) (string, error) {
		command, _, err := engine.CommandContext(context.Background(), CommandSpec{
			Name: "/bin/sh",
			Args: []string{"-c", script},
			Dir:  root,
		})
		if err != nil {
			return "", err
		}
		out, runErr := command.CombinedOutput()
		return string(out), runErr
	}

	for _, script := range []string{"echo hi > /dev/null", "mktemp"} {
		if out, err := run(script); err != nil {
			t.Fatalf("sandboxed %q failed: %v\noutput: %s", script, err, out)
		}
	}

	// The workspace remains writable; a sibling write still lands.
	if out, err := run("echo ok > probe.txt && cat probe.txt"); err != nil {
		t.Fatalf("workspace write failed: %v\noutput: %s", err, out)
	}

	// A sandboxed script must be able to kill the children it spawns; without the
	// signal allowance seatbelt denies kill() with "Operation not permitted".
	if out, err := run("sleep 5 & child=$!; sleep 0.2; kill $child"); err != nil {
		t.Fatalf("sandboxed self-kill failed (signal allowance missing?): %v\noutput: %s", err, out)
	}

	// A write outside the workspace must still be denied — the richer profile must
	// not have loosened the boundary.
	if out, err := run("echo leak > /etc/zero_sandbox_should_fail 2>/dev/null"); err == nil {
		t.Fatalf("write outside workspace unexpectedly succeeded: output: %s", out)
	}
}

func resolvedTestPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", path, err)
	}
	return resolved
}

func TestSandboxExecProfileIncludesExtraWriteRoots(t *testing.T) {
	profile := sandboxExecProfile([]string{"/ws", "/extra root"}, Policy{Mode: ModeEnforce, EnforceWorkspace: true}, "", "", "")
	if !strings.Contains(profile, "(allow file-write*") {
		t.Fatalf("profile missing file-write rule:\n%s", profile)
	}
	// Every granted write root is its own (subpath ...) filter.
	for _, root := range []string{"/ws", "/extra root"} {
		if !strings.Contains(profile, `(subpath "`+root+`")`) {
			t.Fatalf("profile missing write root %q:\n%s", root, profile)
		}
	}
	// The baseline temp tree + standard device nodes (parity with the bubblewrap
	// backend) are kept alongside the granted roots.
	if !strings.Contains(profile, `(subpath "/tmp")`) || !strings.Contains(profile, `(literal "/dev/null")`) {
		t.Fatalf("profile missing baseline temp/device write allowances:\n%s", profile)
	}
}

func TestSandboxExecProfileTagsDenialsWhenMonitoring(t *testing.T) {
	off := sandboxExecProfile([]string{"/ws"}, Policy{Mode: ModeEnforce, EnforceWorkspace: true}, "", "", "")
	if strings.Contains(off, "with message") {
		t.Fatalf("denials must not be tagged when monitoring is off:\n%s", off)
	}
	if !strings.Contains(off, "(deny default)") {
		t.Fatalf("profile missing the plain default-deny:\n%s", off)
	}

	on := sandboxExecProfile([]string{"/ws"}, Policy{Mode: ModeEnforce, EnforceWorkspace: true, MonitorDenials: true}, "", "", "run-tag-123")
	if !strings.Contains(on, `(deny default (with message "run-tag-123"))`) {
		t.Fatalf("denials must be tagged when monitoring is on:\n%s", on)
	}
}

func TestSandboxExecCommandPlanUsesUniquePerPlanDenialTag(t *testing.T) {
	policy := Policy{Mode: ModeEnforce, EnforceWorkspace: true, MonitorDenials: true}
	backend := Backend{Name: BackendSandboxExec, Available: true, Executable: "/usr/bin/sandbox-exec"}
	spec := CommandSpec{Name: "/bin/sh", Args: []string{"-c", "true"}, Dir: "/ws"}

	p1 := sandboxExecCommandPlan(spec, "/ws", []string{"/ws"}, policy, backend, nil)
	p2 := sandboxExecCommandPlan(spec, "/ws", []string{"/ws"}, policy, backend, nil)
	if p1.MonitorTag == "" || p2.MonitorTag == "" {
		t.Fatalf("monitored plans must carry a denial tag: %q %q", p1.MonitorTag, p2.MonitorTag)
	}
	if p1.MonitorTag == p2.MonitorTag {
		t.Fatalf("each monitored plan must get a unique tag so monitors can't cross-ingest, both = %q", p1.MonitorTag)
	}
	// The profile embedded in each plan must carry that plan's own tag (the monitor
	// matches on it).
	if !strings.Contains(strings.Join(p1.Args, " "), p1.MonitorTag) {
		t.Fatalf("plan profile must embed its own tag %q:\n%v", p1.MonitorTag, p1.Args)
	}

	off := sandboxExecCommandPlan(spec, "/ws", []string{"/ws"}, Policy{Mode: ModeEnforce, EnforceWorkspace: true}, backend, nil)
	if off.MonitorTag != "" {
		t.Fatalf("a non-monitored plan must carry no tag, got %q", off.MonitorTag)
	}
}

func TestSandboxExecProfileGrantsSignalAndMachLookup(t *testing.T) {
	profile := sandboxExecProfile([]string{"/ws"}, Policy{Mode: ModeEnforce, EnforceWorkspace: true}, "", "", "")

	// Signalling own process group lets a sandboxed script kill the children it
	// spawns; without it seatbelt denies kill() with "Operation not permitted".
	if !strings.Contains(profile, "(allow signal (target self) (target pgrp))") {
		t.Fatalf("profile missing signal allowance:\n%s", profile)
	}
	// Curated mach-lookup so keychain/opendirectory/preferences/network-config
	// tools work without touching the file or network boundary.
	if !strings.Contains(profile, "(allow mach-lookup") {
		t.Fatalf("profile missing mach-lookup rule:\n%s", profile)
	}
	for _, service := range []string{
		"com.apple.securityd",
		"com.apple.system.opendirectoryd.libinfo",
		"com.apple.cfprefsd.daemon",
	} {
		if !strings.Contains(profile, `(global-name "`+service+`")`) {
			t.Fatalf("profile missing mach service %q:\n%s", service, profile)
		}
	}
	// The security boundary must remain: default-deny plus scoped file-write.
	if !strings.Contains(profile, "(deny default)") || !strings.Contains(profile, "(allow file-write*\n") {
		t.Fatalf("profile lost its default-deny / scoped-write boundary:\n%s", profile)
	}
}

func TestBubblewrapPlanBindsExtraWriteRoots(t *testing.T) {
	workspace := t.TempDir()
	extra := t.TempDir()
	scope, err := NewScope(workspace, []string{extra})
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: workspace,
		Policy:        DefaultPolicy(),
		Scope:         scope,
		Backend:       Backend{Name: BackendBubblewrap, Available: true, Executable: "/usr/bin/bwrap"},
	})
	plan, err := engine.BuildCommandPlan(CommandSpec{Name: "true"})
	if err != nil {
		t.Fatalf("BuildCommandPlan: %v", err)
	}
	joined := strings.Join(plan.Args, " ")
	resolvedExtra := scope.Roots()[1]
	if !strings.Contains(joined, "--bind "+resolvedExtra+" "+resolvedExtra) {
		t.Fatalf("bubblewrap args missing rw bind for extra root %q:\n%s", resolvedExtra, joined)
	}
}

func TestBubblewrapPlanPrefixesSeccompHelperWhenBlockingUnixSockets(t *testing.T) {
	const fakeHelper = "/opt/zero/bin/zero-seccomp"
	original := seccompHelper
	seccompHelper = func() string { return fakeHelper }
	defer func() { seccompHelper = original }()

	workspace := t.TempDir()
	policy := DefaultPolicy()
	policy.BlockUnixSockets = true
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: workspace,
		Policy:        policy,
		Backend:       Backend{Name: BackendBubblewrap, Available: true, Executable: "/usr/bin/bwrap"},
	})
	plan, err := engine.BuildCommandPlan(CommandSpec{Name: "/bin/sh", Args: []string{"-c", "true"}})
	if err != nil {
		t.Fatalf("BuildCommandPlan: %v", err)
	}
	joined := strings.Join(plan.Args, " ")
	if !strings.Contains(joined, "--ro-bind "+fakeHelper+" "+fakeHelper) {
		t.Fatalf("args missing ro-bind for seccomp helper:\n%s", joined)
	}
	// The helper must be the argv that follows the bwrap "--" separator, ahead of
	// the real command, so it wraps execution.
	if !strings.Contains(joined, "-- "+fakeHelper+" /bin/sh -c true") {
		t.Fatalf("seccomp helper not prefixed before the command:\n%s", joined)
	}
}

func TestBubblewrapPlanNoSeccompHelperByDefault(t *testing.T) {
	original := seccompHelper
	seccompHelper = func() string { return "/should/not/be/used/zero-seccomp" }
	defer func() { seccompHelper = original }()

	workspace := t.TempDir()
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: workspace,
		Policy:        DefaultPolicy(), // BlockUnixSockets is false by default
		Backend:       Backend{Name: BackendBubblewrap, Available: true, Executable: "/usr/bin/bwrap"},
	})
	plan, err := engine.BuildCommandPlan(CommandSpec{Name: "/bin/sh", Args: []string{"-c", "true"}})
	if err != nil {
		t.Fatalf("BuildCommandPlan: %v", err)
	}
	joined := strings.Join(plan.Args, " ")
	if strings.Contains(joined, "zero-seccomp") {
		t.Fatalf("seccomp helper must not appear when BlockUnixSockets is off:\n%s", joined)
	}
	if !strings.Contains(joined, "-- /bin/sh -c true") {
		t.Fatalf("command not wired as the plain argv after --:\n%s", joined)
	}
}

func TestResolveCommandDirAllowsExtraRootCwd(t *testing.T) {
	workspace := t.TempDir()
	extra := t.TempDir()
	scope, err := NewScope(workspace, []string{extra})
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	engine := NewEngine(EngineOptions{WorkspaceRoot: workspace, Policy: DefaultPolicy(), Scope: scope})
	if _, _, _, err := engine.resolveCommandDir(extra, engine.policy); err != nil {
		t.Fatalf("resolveCommandDir(extra root) = %v, want nil", err)
	}
	if _, _, _, err := engine.resolveCommandDir(t.TempDir(), engine.policy); err == nil {
		t.Fatal("resolveCommandDir(outside all roots) = nil error, want violation")
	}
}

func TestBubblewrapPlanChdirsToRealPathForExtraRootCwd(t *testing.T) {
	workspace := t.TempDir()
	extra := t.TempDir()
	scope, err := NewScope(workspace, []string{extra})
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: workspace,
		Policy:        DefaultPolicy(),
		Scope:         scope,
		Backend:       Backend{Name: BackendBubblewrap, Available: true, Executable: "/usr/bin/bwrap"},
	})
	resolvedExtra := scope.Roots()[1]
	plan, err := engine.BuildCommandPlan(CommandSpec{Name: "true", Dir: extra})
	if err != nil {
		t.Fatalf("BuildCommandPlan: %v", err)
	}
	if plan.SandboxDir != filepath.ToSlash(resolvedExtra) {
		t.Fatalf("SandboxDir=%q want real extra-root path %q", plan.SandboxDir, resolvedExtra)
	}
	joined := strings.Join(plan.Args, " ")
	if !strings.Contains(joined, "--chdir "+filepath.ToSlash(resolvedExtra)) {
		t.Fatalf("args missing --chdir to real extra-root path:\n%s", joined)
	}
	// The workspace must appear only at its /workspace remount, never
	// double-bound at its real host path.
	resolvedWorkspace := scope.Roots()[0]
	if strings.Contains(joined, "--bind "+resolvedWorkspace+" "+resolvedWorkspace) {
		t.Fatalf("workspace double-bound at real path:\n%s", joined)
	}
}

func TestProxyEnv(t *testing.T) {
	env := map[string]string{}
	for _, kv := range ProxyEnv("127.0.0.1:8899") {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			env[kv[:i]] = kv[i+1:]
		}
	}
	const proxyURL = "http://127.0.0.1:8899"
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"} {
		if env[key] != proxyURL {
			t.Fatalf("ProxyEnv[%s] = %q, want %q", key, env[key], proxyURL)
		}
		if lower := strings.ToLower(key); env[lower] != proxyURL {
			t.Fatalf("ProxyEnv missing lowercase %s=%q", lower, proxyURL)
		}
	}
	// Loopback must bypass the proxy so the proxy itself is reachable directly.
	if !strings.Contains(env["NO_PROXY"], "127.0.0.1") || !strings.Contains(env["no_proxy"], "127.0.0.1") {
		t.Fatalf("ProxyEnv NO_PROXY must exclude loopback, got %q/%q", env["NO_PROXY"], env["no_proxy"])
	}
}

func TestProxyEnvWithSocks(t *testing.T) {
	env := map[string]string{}
	for _, kv := range ProxyEnvWithSocks("127.0.0.1:8899", "127.0.0.1:1080") {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			env[kv[:i]] = kv[i+1:]
		}
	}
	// HTTP_PROXY/HTTPS_PROXY stay on the HTTP listener...
	const httpURL = "http://127.0.0.1:8899"
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		if env[key] != httpURL {
			t.Fatalf("ProxyEnvWithSocks[%s] = %q, want %q", key, env[key], httpURL)
		}
	}
	// ...while ALL_PROXY/all_proxy point at the SOCKS5 front-end.
	const socksURL = "socks5://127.0.0.1:1080"
	for _, key := range []string{"ALL_PROXY", "all_proxy"} {
		if env[key] != socksURL {
			t.Fatalf("ProxyEnvWithSocks[%s] = %q, want %q", key, env[key], socksURL)
		}
	}
	if !strings.Contains(env["NO_PROXY"], "127.0.0.1") {
		t.Fatalf("ProxyEnvWithSocks NO_PROXY must exclude loopback, got %q", env["NO_PROXY"])
	}
}

// TestProxyEnvWithSocksEmptyFallsBackToHTTP verifies the SOCKS upgrade is purely
// additive: with no SOCKS address, ALL_PROXY falls back to the HTTP proxy exactly
// as the legacy ProxyEnv produced, never weakening the default.
func TestProxyEnvWithSocksEmptyFallsBackToHTTP(t *testing.T) {
	env := map[string]string{}
	for _, kv := range ProxyEnvWithSocks("127.0.0.1:8899", "") {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			env[kv[:i]] = kv[i+1:]
		}
	}
	const httpURL = "http://127.0.0.1:8899"
	for _, key := range []string{"ALL_PROXY", "all_proxy"} {
		if env[key] != httpURL {
			t.Fatalf("ProxyEnvWithSocks(empty socks)[%s] = %q, want HTTP fallback %q", key, env[key], httpURL)
		}
	}
}
