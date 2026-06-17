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

func TestBuildCommandPlanWrapsLinuxHelper(t *testing.T) {
	root := t.TempDir()
	resolvedRoot := resolvedTestPath(t, root)
	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	resolvedNested := resolvedTestPath(t, nested)
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: root,
		Policy:        DefaultPolicy(),
		Backend: Backend{
			Name:       BackendLinuxBwrap,
			Available:  true,
			Executable: "/usr/bin/zero-linux-sandbox",
			Platform:   "linux",
			Message:    "Linux sandbox helper available",
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

	if !plan.Wrapped || plan.Name != "/usr/bin/zero-linux-sandbox" || plan.Backend.Name != BackendLinuxBwrap {
		t.Fatalf("plan backend = %#v, want wrapped Linux helper", plan)
	}
	assertArgsContainSequence(t, plan.Args, "--sandbox-policy-cwd", resolvedRoot)
	assertArgsContainSequence(t, plan.Args, "--command-cwd", resolvedNested)
	assertArgsContainSequence(t, plan.Args, "--", "/bin/sh", "-c", "pwd")
	if plan.SandboxDir != resolvedNested {
		t.Fatalf("SandboxDir = %q, want command cwd", plan.SandboxDir)
	}
	if plan.Dir != resolvedNested {
		t.Fatalf("helper host Dir = %q, want command cwd", plan.Dir)
	}
}

func TestBuildCommandPlanWrapsSandboxExec(t *testing.T) {
	root := t.TempDir()
	resolvedRoot := resolvedTestPath(t, root)
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: root,
		Policy:        DefaultPolicy(),
		Backend: Backend{
			Name:       BackendMacOSSeatbelt,
			Available:  true,
			Executable: "/usr/bin/sandbox-exec",
			Platform:   "darwin",
			Message:    "macOS Seatbelt backend available",
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

	if !plan.Wrapped || plan.Name != "/usr/bin/sandbox-exec" || plan.Backend.Name != BackendMacOSSeatbelt {
		t.Fatalf("plan backend = %#v, want wrapped macOS Seatbelt", plan)
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
	if !backend.Available || backend.Name != BackendMacOSSeatbelt {
		t.Skipf("macOS Seatbelt backend unavailable: %s", backend.Message)
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

func TestSeatbeltProfileConsumesPermissionProfile(t *testing.T) {
	profile := PermissionProfile{
		FileSystem: FileSystemPolicy{
			Kind:      FileSystemRestricted,
			ReadRoots: []string{"/read-root"},
			WriteRoots: []WritableRoot{{
				Root: "/write-root",
			}},
			IncludePlatformRoots: true,
			AllowTemp:            true,
		},
		Network: NetworkPolicy{Mode: NetworkDeny},
	}
	sbpl := seatbeltProfileFromPermissionProfile(profile, Policy{Mode: ModeEnforce}, "", "", "")
	for _, want := range []string{
		`(subpath "/read-root")`,
		`(subpath "/write-root")`,
		`(subpath "/usr/bin")`,
		`(subpath "/System/Library/Frameworks")`,
		`(subpath "/private/var/db")`,
		`(subpath "/tmp")`,
		`(literal "/dev/null")`,
		`(deny network*)`,
	} {
		if !strings.Contains(sbpl, want) {
			t.Fatalf("Seatbelt profile missing %q:\n%s", want, sbpl)
		}
	}
	if strings.Contains(sbpl, "(allow file-read*)\n(allow file-write*)") {
		t.Fatalf("restricted permission profile must not become full read/write:\n%s", sbpl)
	}
}

func TestSeatbeltProfileIncludesRuntimeStartupAllowances(t *testing.T) {
	sbpl := sandboxExecProfile([]string{"/ws"}, Policy{Mode: ModeEnforce, EnforceWorkspace: true}, "", "", "")
	for _, want := range []string{
		`(allow file-map-executable`,
		`(subpath "/System/Library/Frameworks")`,
		`(allow system-mac-syscall (mac-policy-name "vnguard"))`,
		`(allow file-read* file-test-existence (literal "/"))`,
		`(allow user-preference-read)`,
		`(allow pseudo-tty)`,
		`(allow ipc-posix-sem)`,
	} {
		if !strings.Contains(sbpl, want) {
			t.Fatalf("Seatbelt profile missing runtime startup allowance %q:\n%s", want, sbpl)
		}
	}
}

func TestSeatbeltCommandPlanUsesExecutionPermissionProfile(t *testing.T) {
	request := SandboxExecutionRequest{
		Command: CommandSpec{Name: "/bin/sh", Args: []string{"-c", "true"}, Dir: "/workspace"},
		Backend: Backend{
			Name:            BackendMacOSSeatbelt,
			Available:       true,
			Executable:      "/usr/bin/sandbox-exec",
			CommandWrapping: true,
			NativeIsolation: true,
		},
		WorkspaceRoot: "/workspace",
		PermissionProfile: PermissionProfile{
			FileSystem: FileSystemPolicy{
				Kind:       FileSystemRestricted,
				ReadRoots:  []string{"/"},
				WriteRoots: []WritableRoot{{Root: "/profile-write"}},
				AllowTemp:  true,
			},
			Network: NetworkPolicy{Mode: NetworkDeny},
		},
		TargetBackend:           BackendMacOSSeatbelt,
		CommandWrapped:          true,
		EnforcementLevel:        EnforcementNative,
		RequiresPlatformSandbox: true,
	}
	plan, err := buildPlatformCommandPlan(request, Policy{Mode: ModeEnforce, EnforceWorkspace: true})
	if err != nil {
		t.Fatalf("buildPlatformCommandPlan: %v", err)
	}
	if len(plan.Args) < 2 {
		t.Fatalf("plan args = %#v, want sandbox-exec profile", plan.Args)
	}
	sbpl := plan.Args[1]
	if !strings.Contains(sbpl, `(subpath "/profile-write")`) {
		t.Fatalf("plan profile did not use PermissionProfile write root:\n%s", sbpl)
	}
}

func TestSeatbeltProfileProtectsMetadataAndDenyOrdering(t *testing.T) {
	profile := PermissionProfile{
		FileSystem: FileSystemPolicy{
			Kind:      FileSystemRestricted,
			ReadRoots: []string{"/"},
			WriteRoots: []WritableRoot{{
				Root:                   "/repo",
				ReadOnlySubpaths:       []string{"/repo/vendor"},
				ProtectedMetadataNames: []string{".git", ".zero"},
			}},
			DenyRead:  []string{"/repo/secret-read"},
			DenyWrite: []string{"/repo/secret-write"},
			AllowTemp: true,
		},
		Network: NetworkPolicy{Mode: NetworkDeny},
	}
	sbpl := seatbeltProfileFromPermissionProfile(profile, Policy{Mode: ModeEnforce}, "", "", "")
	normalizedSecretRead := sandboxProfileString(normalizeProfilePath("/repo/secret-read"))
	normalizedSecretWrite := sandboxProfileString(normalizeProfilePath("/repo/secret-write"))
	denySecretReadRule := `(deny file-read* (subpath "` + normalizedSecretRead + `"))`
	denySecretReadUnlinkRule := `(deny file-write-unlink (subpath "` + normalizedSecretRead + `"))`
	denySecretWriteRule := `(deny file-write* (subpath "` + normalizedSecretWrite + `"))`
	for _, want := range []string{
		`(deny file-write* (literal "/repo/vendor"))`,
		`(deny file-write* (subpath "/repo/vendor"))`,
		`(deny file-write* (regex #"^/repo/\.git(/.*)?$"))`,
		`(deny file-write* (regex #"^/repo/\.zero(/.*)?$"))`,
		denySecretReadRule,
		denySecretReadUnlinkRule,
		denySecretWriteRule,
	} {
		if !strings.Contains(sbpl, want) {
			t.Fatalf("Seatbelt profile missing %q:\n%s", want, sbpl)
		}
	}
	allowIdx := strings.Index(sbpl, "(allow file-write*")
	denyReadIdx := strings.Index(sbpl, denySecretReadRule)
	metadataIdx := strings.Index(sbpl, `(deny file-write* (regex #"^/repo/\.git(/.*)?$"))`)
	denyWriteIdx := strings.Index(sbpl, denySecretWriteRule)
	if allowIdx < 0 || denyReadIdx < allowIdx || metadataIdx < allowIdx || denyWriteIdx < allowIdx {
		t.Fatalf("deny rules must follow the broad write allow (allow=%d denyRead=%d metadata=%d denyWrite=%d):\n%s", allowIdx, denyReadIdx, metadataIdx, denyWriteIdx, sbpl)
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
	backend := Backend{Name: BackendMacOSSeatbelt, Available: true, Executable: "/usr/bin/sandbox-exec"}
	spec := CommandSpec{Name: "/bin/sh", Args: []string{"-c", "true"}, Dir: "/ws"}
	profile := seatbeltCompatibilityPermissionProfile([]string{"/ws"}, policy)

	p1 := seatbeltCommandPlanWithProfile(spec, "/ws", profile, policy, backend, nil)
	p2 := seatbeltCommandPlanWithProfile(spec, "/ws", profile, policy, backend, nil)
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

	offPolicy := Policy{Mode: ModeEnforce, EnforceWorkspace: true}
	off := seatbeltCommandPlanWithProfile(spec, "/ws", seatbeltCompatibilityPermissionProfile([]string{"/ws"}, offPolicy), offPolicy, backend, nil)
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

func TestLinuxHelperPlanCarriesExtraWriteRoots(t *testing.T) {
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
		Backend:       Backend{Name: BackendLinuxBwrap, Available: true, Executable: "/usr/bin/zero-linux-sandbox"},
	})
	plan, err := engine.BuildCommandPlan(CommandSpec{Name: "true"})
	if err != nil {
		t.Fatalf("BuildCommandPlan: %v", err)
	}
	config, err := ParseLinuxSandboxHelperArgs(plan.Args)
	if err != nil {
		t.Fatalf("ParseLinuxSandboxHelperArgs: %v", err)
	}
	resolvedExtra := scope.Roots()[1]
	if len(config.PermissionProfile.FileSystem.WriteRoots) < 2 || config.PermissionProfile.FileSystem.WriteRoots[1].Root != resolvedExtra {
		t.Fatalf("helper profile missing extra write root %q: %#v", resolvedExtra, config.PermissionProfile.FileSystem.WriteRoots)
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
	if _, _, err := engine.resolveCommandDir(extra, engine.policy); err != nil {
		t.Fatalf("resolveCommandDir(extra root) = %v, want nil", err)
	}
	if _, _, err := engine.resolveCommandDir(t.TempDir(), engine.policy); err == nil {
		t.Fatal("resolveCommandDir(outside all roots) = nil error, want violation")
	}
}

func TestLinuxHelperPlanPreservesRealExtraRootCwd(t *testing.T) {
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
		Backend:       Backend{Name: BackendLinuxBwrap, Available: true, Executable: "/usr/bin/zero-linux-sandbox"},
	})
	resolvedExtra := scope.Roots()[1]
	plan, err := engine.BuildCommandPlan(CommandSpec{Name: "true", Dir: extra})
	if err != nil {
		t.Fatalf("BuildCommandPlan: %v", err)
	}
	if filepath.Clean(plan.SandboxDir) != filepath.Clean(resolvedExtra) {
		t.Fatalf("SandboxDir=%q want real extra-root path %q", plan.SandboxDir, resolvedExtra)
	}
	assertArgsContainSequence(t, plan.Args, "--command-cwd", resolvedExtra)
	config, err := ParseLinuxSandboxHelperArgs(plan.Args)
	if err != nil {
		t.Fatalf("ParseLinuxSandboxHelperArgs: %v", err)
	}
	bwrapArgs, err := BuildLinuxSandboxBwrapArgs(LinuxSandboxBwrapOptions{Config: config, HelperPath: "/usr/bin/zero-linux-sandbox"})
	if err != nil {
		t.Fatalf("BuildLinuxSandboxBwrapArgs: %v", err)
	}
	assertArgsContainSequence(t, bwrapArgs, "--chdir", resolvedExtra)
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
