package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
)

var errPolicyOnlyRunnerDisabled = errors.New("policy-only sandbox runner is disabled")

// errWSLPolicyOnlyDisabled is returned when running under WSL without bubblewrap
// and the policy has NOT opted into the policy-only runner — fail closed rather
// than run with no native isolation.
var errWSLPolicyOnlyDisabled = errors.New("bubblewrap is unavailable under WSL and Policy.AllowPolicyOnlyRunner is disabled: " +
	"enable AllowPolicyOnlyRunner to run under the policy-only WSL fallback (engine policy + proxy egress, no native isolation), " +
	"or install/enable bubblewrap")

type CommandSpec struct {
	Name string
	Args []string
	Dir  string
	Env  []string
}

type CommandPlan struct {
	Backend                 Backend           `json:"backend"`
	TargetBackend           BackendName       `json:"targetBackend"`
	WorkspaceRoot           string            `json:"workspaceRoot"`
	Policy                  Policy            `json:"policy"`
	PermissionProfile       PermissionProfile `json:"permissionProfile"`
	Wrapped                 bool              `json:"wrapped"`
	SandboxEnvMarkers       []string          `json:"sandboxEnvMarkers,omitempty"`
	EnforcementLevel        EnforcementLevel  `json:"enforcementLevel"`
	DowngradeReason         string            `json:"downgradeReason,omitempty"`
	RequiresPlatformSandbox bool              `json:"requiresPlatformSandbox"`
	Name                    string            `json:"name"`
	Args                    []string          `json:"args"`
	Dir                     string            `json:"dir,omitempty"`
	Env                     []string          `json:"env,omitempty"`
	SandboxDir              string            `json:"sandboxDir,omitempty"`
	// MonitorTag, when non-empty, is the unique marker embedded in the
	// sandbox-exec profile's denial messages; a caller passes it to
	// StartDenialMonitor to capture what the sandbox blocked. Empty unless
	// Policy.MonitorDenials is set on a macOS sandbox-exec plan.
	MonitorTag string `json:"monitorTag,omitempty"`
	// Notes records auditable least-privilege downgrade notes — e.g. the WSL
	// policy-only fallback noting that native isolation was unavailable. Surfaced to
	// the operator; never affects enforcement.
	Notes []string `json:"notes,omitempty"`
	// cleanup releases resources tied to the plan's lifetime — currently the
	// scoped-egress proxy, which must outlive the command run and be shut down
	// afterwards. It is never serialized; callers invoke it via Cleanup() once the
	// command has finished.
	cleanup func()
}

// Cleanup releases any resources the plan holds (e.g. a scoped-egress proxy). It
// is safe to call on a zero plan and to call more than once. Callers that run a
// plan's command must defer Cleanup so a started proxy does not leak.
func (plan CommandPlan) Cleanup() {
	if plan.cleanup != nil {
		plan.cleanup()
	}
}

// startEgressProxy is the constructor for the scoped-egress proxy, kept as a
// package var so tests can force a start failure and assert the build fails
// closed (never degrading to open network).
var startEgressProxy = newEgressProxy

// effectiveNetwork resolves the network mode actually enforced for a policy.
// NetworkScoped with no usable allowlisted domains collapses to NetworkDeny so
// scoped egress fails closed; NetworkAllow and NetworkDeny are returned as-is.
func effectiveNetwork(policy Policy) NetworkMode {
	if policy.Network == NetworkScoped && len(normalizeDomains(policy.AllowedDomains)) == 0 {
		return NetworkDeny
	}
	return policy.Network
}

// ProxyEnv returns the proxy environment variables that route a process's
// HTTP(S) traffic through the local filtering proxy at addr. It is the single
// source of truth for proxy-env injection so every network-capable child (the
// sandboxed shell today; MCP spawns and others when wired to a session proxy)
// uses identical settings. Both upper- and lower-case forms are set because
// different clients read different casings; loopback is excluded via NO_PROXY so
// the proxy itself is reached directly.
//
// Note: clients that honor these vars include Go's default HTTP transport (so the
// web_fetch tool, which clones http.DefaultTransport, already routes through a
// configured proxy) and MCP child processes (mergeProcessEnv inherits os.Environ).
// Routing those through a SCOPED proxy therefore only needs a session-level proxy
// whose address is exposed to the agent process — but that must allowlist the
// active LLM provider's domain first, or the agent's own provider calls would be
// blocked. That session-proxy lifecycle is intentionally not wired here.
func ProxyEnv(addr string) []string {
	proxyURL := "http://" + addr
	return []string{
		"HTTP_PROXY=" + proxyURL,
		"HTTPS_PROXY=" + proxyURL,
		"ALL_PROXY=" + proxyURL,
		"http_proxy=" + proxyURL,
		"https_proxy=" + proxyURL,
		"all_proxy=" + proxyURL,
		"NO_PROXY=localhost,127.0.0.1",
		"no_proxy=localhost,127.0.0.1",
	}
}

// ProxyEnvWithSocks is the SOCKS-aware form of ProxyEnv built ON TOP of it:
// HTTP_PROXY/HTTPS_PROXY stay on the HTTP listener (httpAddr) for HTTP(S)-aware
// clients, while ALL_PROXY/all_proxy are re-pointed at the SOCKS5 front-end
// (socksAddr) so a client that honors ALL_PROXY tunnels arbitrary TCP through the
// same allow/deny gate. When socksAddr is empty it returns exactly ProxyEnv's
// output, so the SOCKS upgrade is purely additive and never weakens the default.
func ProxyEnvWithSocks(httpAddr, socksAddr string) []string {
	env := ProxyEnv(httpAddr)
	if socksAddr == "" {
		return env
	}
	socksURL := "socks5://" + socksAddr
	for i, kv := range env {
		switch {
		case strings.HasPrefix(kv, "ALL_PROXY="):
			env[i] = "ALL_PROXY=" + socksURL
		case strings.HasPrefix(kv, "all_proxy="):
			env[i] = "all_proxy=" + socksURL
		}
	}
	return env
}

func (engine *Engine) CommandContext(ctx context.Context, spec CommandSpec) (*exec.Cmd, CommandPlan, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	plan, err := engine.BuildCommandPlan(spec)
	if err != nil {
		return nil, CommandPlan{}, err
	}
	command := exec.CommandContext(ctx, plan.Name, plan.Args...)
	command.Dir = plan.Dir
	command.Env = plan.Env
	return command, plan, nil
}

// writeRoots returns the full ordered write-root list for command plans:
// the workspace root plus any granted extra roots. The single-root fallback
// only applies to engines built without a workspace root (NewEngine always
// builds a scope otherwise); it is kept as defense in depth.
func (engine *Engine) writeRoots(workspaceRoot string) []string {
	var roots []string
	if engine.scope != nil {
		roots = engine.scope.Roots()
	} else {
		roots = []string{workspaceRoot}
	}
	// Reflect the policy's AllowWrite roots in the OS backend write binds so a
	// sandboxed shell command may write where the policy grants writes. DenyWrite
	// is enforced at the policy gate, and on sandbox-exec additionally as an
	// explicit deny rule (see sandboxExecProfile).
	policy := engine.effectivePolicy(engine.policy)
	if extra := resolveWriteRootPaths(policy.AllowWrite); len(extra) > 0 {
		roots = dedupeStrings(append(roots, extra...))
	}
	return roots
}

func (engine *Engine) BuildCommandPlan(spec CommandSpec) (CommandPlan, error) {
	if engine == nil {
		return directCommandPlan(spec, Backend{Name: BackendPolicyOnly, Message: "sandbox disabled"}, Policy{}, ""), nil
	}
	policy := engine.effectivePolicy(engine.policy)
	if policy.Mode == "" {
		policy = DefaultPolicy()
	}
	workspaceRoot, commandDir, err := engine.resolveCommandDir(spec.Dir, policy)
	if err != nil {
		return CommandPlan{}, err
	}
	spec.Name = strings.TrimSpace(spec.Name)
	if spec.Name == "" {
		return CommandPlan{}, errors.New("sandbox command name is required")
	}
	spec.Dir = commandDir

	backend := engine.backend
	if backend.Name == "" {
		backend = Backend{Name: BackendPolicyOnly, Message: "policy-only fallback: sandbox backend was not selected"}
	}
	preference := SandboxPreferenceAuto
	// Re-entrancy guard: a command spawned by a process we already wrapped (both
	// ZERO_SANDBOXED=1 and ZERO_SANDBOX_BACKEND set in its env — see
	// IsAlreadySandboxed) must not be wrapped again — nested platform wrappers
	// fails and a second egress proxy would be redundant. Return a pass-through
	// plan.
	if IsAlreadySandboxed() {
		preference = SandboxPreferenceForbid
	}
	if policy.Mode == ModeDisabled {
		preference = SandboxPreferenceForbid
	}
	profile := PermissionProfileFromPolicy(workspaceRoot, policy, engine.scope)
	manager := NewSandboxManager(SandboxManagerOptions{
		GOOS:    backend.Platform,
		Backend: backend,
	})
	return manager.BuildCommandPlan(SandboxManagerRequest{
		WorkspaceRoot:     workspaceRoot,
		Command:           spec,
		Policy:            policy,
		Scope:             engine.scope,
		Profile:           profile,
		Preference:        preference,
		ValidateExecution: true,
	})
}

func buildPlatformCommandPlan(execRequest SandboxExecutionRequest, policy Policy) (CommandPlan, error) {
	if policy.Mode == "" {
		policy = DefaultPolicy()
	}
	spec := execRequest.Command
	backend := execRequest.Backend
	workspaceRoot := execRequest.WorkspaceRoot
	if execRequest.EnforcementLevel == EnforcementDisabled || execRequest.TargetBackend == BackendNone || !execRequest.RequiresPlatformSandbox {
		return withSandboxExecutionMetadata(directCommandPlan(spec, backend, policy, workspaceRoot), execRequest), nil
	}
	switch backend.Name {
	case BackendLinuxBwrap:
		if backend.Available && backend.Executable != "" {
			return linuxSandboxHelperCommandPlan(execRequest, policy)
		}
	case BackendMacOSSeatbelt:
		if backend.Available && backend.Executable != "" {
			egress, err := startScopedEgress(policy, backend)
			if err != nil {
				return CommandPlan{}, err
			}
			return withSandboxExecutionMetadata(seatbeltCommandPlan(execRequest, policy, backend, egress), execRequest), nil
		}
	case BackendWindowsRestrictedToken:
		if backend.Available && backend.Executable != "" {
			return windowsRestrictedTokenCommandPlan(execRequest, policy)
		}
	case BackendWSL:
		// WSL fallback: no native isolation available. Fail closed unless the policy
		// opted into the policy-only runner; otherwise run policy-only with the
		// command's egress routed through the filtering proxy and an auditable note.
		if !policy.AllowPolicyOnlyRunner {
			return CommandPlan{}, errWSLPolicyOnlyDisabled
		}
		egress, err := startScopedEgress(policy, backend)
		if err != nil {
			return CommandPlan{}, err
		}
		return withSandboxExecutionMetadata(wslCommandPlan(spec, workspaceRoot, policy, backend, egress), execRequest), nil
	}
	if !policy.AllowPolicyOnlyRunner {
		return CommandPlan{}, errPolicyOnlyRunnerDisabled
	}
	return withSandboxExecutionMetadata(directCommandPlan(spec, backend, policy, workspaceRoot), execRequest), nil
}

func linuxSandboxHelperCommandPlan(execRequest SandboxExecutionRequest, policy Policy) (CommandPlan, error) {
	spec := execRequest.Command
	helper := LinuxSandboxHelperCommand{}
	if execRequest.Backend.Name == BackendLinuxBwrap && execRequest.Backend.Executable != "" {
		helper.Name = execRequest.Backend.Executable
	} else {
		resolved, err := linuxSandboxHelperCommand()
		if err != nil {
			return CommandPlan{}, err
		}
		helper = resolved
	}
	command := append([]string{spec.Name}, spec.Args...)
	args, err := BuildLinuxSandboxCommandArgs(LinuxSandboxCommandArgsOptions{
		SandboxPolicyCWD:  execRequest.WorkspaceRoot,
		CommandCWD:        spec.Dir,
		PermissionProfile: execRequest.PermissionProfile,
		BlockUnixSockets:  policy.BlockUnixSockets,
		Command:           command,
	})
	if err != nil {
		return CommandPlan{}, err
	}
	planDir := spec.Dir
	if helper.Dir != "" {
		planDir = helper.Dir
	}
	plan := CommandPlan{
		Backend:           execRequest.Backend,
		TargetBackend:     execRequest.TargetBackend,
		WorkspaceRoot:     execRequest.WorkspaceRoot,
		Policy:            policy,
		Wrapped:           true,
		SandboxEnvMarkers: execRequest.SandboxEnvMarkers,
		EnforcementLevel:  execRequest.EnforcementLevel,
		Name:              helper.Name,
		Args:              append(append([]string{}, helper.ArgsPrefix...), args...),
		Dir:               planDir,
		Env:               cloneStrings(spec.Env),
		SandboxDir:        spec.Dir,
	}
	return withSandboxExecutionMetadata(plan, execRequest), nil
}

func withSandboxExecutionMetadata(plan CommandPlan, request SandboxExecutionRequest) CommandPlan {
	plan.Backend = request.Backend
	plan.TargetBackend = request.TargetBackend
	plan.PermissionProfile = request.PermissionProfile
	plan.SandboxEnvMarkers = request.SandboxEnvMarkers
	plan.EnforcementLevel = request.EnforcementLevel
	plan.DowngradeReason = request.DowngradeReason
	plan.RequiresPlatformSandbox = request.RequiresPlatformSandbox
	return plan
}

// wslCommandPlan builds the WSL policy-only fallback plan: the command runs
// DIRECTLY (no native wrap, since bubblewrap is unavailable under WSL) but with
// the sandbox env markers (ZERO_SANDBOXED=1, ZERO_SANDBOX_BACKEND=wsl) and, when
// a scoped-egress proxy was started, the proxy env so well-behaved clients route
// through the allow/deny gate. It carries a least-privilege downgrade note.
func wslCommandPlan(spec CommandSpec, workspaceRoot string, policy Policy, backend Backend, egress *scopedEgress) CommandPlan {
	// The WSL fallback runs the command DIRECTLY (no native wrap), so it inherits
	// the caller's environment like the generic policy-only runner; the sandbox
	// markers are appended last so they cannot be shadowed by the caller's env.
	var env []string
	if spec.Env != nil {
		env = cloneStrings(spec.Env)
	} else {
		env = append(env, os.Environ()...)
	}
	env = append(env,
		EnvSandboxBackend+"="+string(BackendWSL),
		"ZERO_SANDBOX_NETWORK="+string(policy.Network),
		EnvSandboxed+"=1",
	)
	note := "WSL: native process isolation (bubblewrap) is unavailable; downgraded to the policy-only runner " +
		"(least privilege). The engine still enforces the full policy."
	if egress != nil {
		env = append(env, ProxyEnvWithSocks(egress.addr, egress.socksAddr)...)
		env = appendCACertEnv(env, egress)
		note = "WSL: native process isolation (bubblewrap) is unavailable; downgraded to the policy-only runner " +
			"with proxy egress (least privilege). The engine still enforces the full policy."
	}
	plan := CommandPlan{
		Backend:           backend,
		TargetBackend:     backend.TargetBackend(),
		WorkspaceRoot:     workspaceRoot,
		Policy:            policy,
		Wrapped:           false,
		SandboxEnvMarkers: backend.SandboxEnvMarkers(policy),
		EnforcementLevel:  backend.EnforcementLevel(policy),
		DowngradeReason:   note,
		Name:              spec.Name,
		Args:              cloneStrings(spec.Args),
		Dir:               spec.Dir,
		Env:               env,
		Notes:             []string{note},
	}
	if egress != nil {
		plan.cleanup = egress.cleanup
	}
	return plan
}

// scopedEgress holds the address of a started scoped-egress proxy and the
// cleanup that shuts it down. A nil *scopedEgress means scoped egress is not in
// effect for this command (the network mode is allow or deny-equivalent).
type scopedEgress struct {
	addr string
	// socksAddr is the loopback address of the proxy's SOCKS5 front-end, used to
	// set ALL_PROXY=socks5://<socksAddr>. Empty when the proxy exposes no SOCKS
	// listener, in which case ALL_PROXY falls back to the HTTP proxy.
	socksAddr string
	// caCertPath is the path to the MITM CA's public cert (PEM), set only when
	// Policy.InspectTLS started a TLS-terminating proxy. Surfaced to the sandboxed
	// client via ZERO_SANDBOX_CA_CERT so it can trust the minted leaves.
	caCertPath string
	cleanup    func()
}

// EnvCACert is the env var pointing the sandboxed client at the MITM CA's public
// cert so it can trust the proxy's minted leaf certificates (only set when
// Policy.InspectTLS is on).
const EnvCACert = "ZERO_SANDBOX_CA_CERT"

// appendCACertEnv adds ZERO_SANDBOX_CA_CERT when the egress proxy is terminating
// TLS (InspectTLS). It is a no-op for a plain passthrough proxy.
func appendCACertEnv(env []string, egress *scopedEgress) []string {
	if egress == nil || egress.caCertPath == "" {
		return env
	}
	return append(env, EnvCACert+"="+egress.caCertPath)
}

// startScopedEgress starts the local filtering proxy when the policy's effective
// network mode is NetworkScoped AND the backend can actually route through it,
// returning its address. It fails closed: a proxy-start error is returned so the
// build aborts rather than degrading to an unproxied (open) plan. A non-scoped or
// empty-allowlist policy, or a backend that cannot enforce scoped egress (e.g.
// bubblewrap's isolated netns), returns (nil, nil); the caller then wires the
// command with the backend's deny-equivalent network isolation.
func startScopedEgress(policy Policy, backend Backend) (*scopedEgress, error) {
	if effectiveNetwork(policy) != NetworkScoped {
		return nil, nil
	}
	if !backend.EnforcesScopedEgress() {
		return nil, nil
	}
	proxy, err := startEgressProxy(egressOptions{
		Allowed:    policy.AllowedDomains,
		Denied:     policy.DeniedDomains,
		InspectTLS: policy.InspectTLS,
	})
	if err != nil {
		return nil, fmt.Errorf("scoped egress unavailable, denying network: %w", err)
	}
	se := &scopedEgress{addr: proxy.Addr(), socksAddr: proxy.SocksAddr(), cleanup: func() { _ = proxy.Close() }}
	if policy.InspectTLS {
		// Write the MITM CA's PUBLIC cert so the sandboxed client can trust the
		// minted leaves; surface it via ZERO_SANDBOX_CA_CERT and clean it up with
		// the proxy. Reuses no new bind path: sandbox-exec already allows file-read,
		// and the WSL policy-only runner shares the host filesystem.
		caPath, werr := writeMITMCAFile(proxy.CACertPEM())
		if werr != nil {
			_ = proxy.Close()
			return nil, werr
		}
		se.caCertPath = caPath
		base := se.cleanup
		se.cleanup = func() {
			base()
			_ = os.Remove(caPath)
		}
	}
	return se, nil
}

func directCommandPlan(spec CommandSpec, backend Backend, policy Policy, workspaceRoot string) CommandPlan {
	return CommandPlan{
		Backend:           backend,
		TargetBackend:     backend.TargetBackend(),
		WorkspaceRoot:     workspaceRoot,
		Policy:            policy,
		Wrapped:           false,
		SandboxEnvMarkers: backend.SandboxEnvMarkers(policy),
		EnforcementLevel:  backend.EnforcementLevel(policy),
		DowngradeReason:   backend.DowngradeReason(policy),
		Name:              spec.Name,
		Args:              cloneStrings(spec.Args),
		Dir:               spec.Dir,
		Env:               cloneStrings(spec.Env),
	}
}

func (engine *Engine) resolveCommandDir(dir string, policy Policy) (string, string, error) {
	workspaceRoot := strings.TrimSpace(engine.workspaceRoot)
	if workspaceRoot == "" {
		return "", "", errors.New("sandbox workspace root is required")
	}
	workspaceRoot = filepath.Clean(workspaceRoot)
	if !filepath.IsAbs(workspaceRoot) {
		absolute, err := filepath.Abs(workspaceRoot)
		if err != nil {
			return "", "", fmt.Errorf("resolve sandbox workspace: %w", err)
		}
		workspaceRoot = absolute
	}
	if resolved, err := filepath.EvalSymlinks(workspaceRoot); err == nil {
		workspaceRoot = resolved
	}

	commandDir := strings.TrimSpace(dir)
	if commandDir == "" {
		commandDir = workspaceRoot
	} else if !filepath.IsAbs(commandDir) {
		commandDir = filepath.Join(workspaceRoot, commandDir)
	}
	commandDir = filepath.Clean(commandDir)
	if resolved, err := filepath.EvalSymlinks(commandDir); err == nil {
		commandDir = resolved
	}
	if policy.EnforceWorkspace {
		if violation := engine.scopeFor(engine.workspaceRoot).validate(commandDir); violation != nil {
			return "", "", Violation{
				Code:     violation.Code,
				ToolName: "sandbox_command",
				Action:   ActionDeny,
				Risk: Risk{
					Level:      RiskCritical,
					Categories: []string{"path_escape"},
					Reason:     "critical risk: path_escape",
				},
				Path:   violation.Path,
				Reason: violation.Reason,
			}
		}
	}
	return workspaceRoot, commandDir, nil
}

func seatbeltCommandPlan(execRequest SandboxExecutionRequest, policy Policy, backend Backend, egress *scopedEgress) CommandPlan {
	return seatbeltCommandPlanWithProfile(execRequest.Command, execRequest.WorkspaceRoot, execRequest.PermissionProfile, policy, backend, egress)
}

func seatbeltCommandPlanWithProfile(spec CommandSpec, workspaceRoot string, profile PermissionProfile, policy Policy, backend Backend, egress *scopedEgress) CommandPlan {
	var proxyPort, socksPort string
	if egress != nil {
		if _, port, err := net.SplitHostPort(egress.addr); err == nil {
			proxyPort = port
		}
		if _, port, err := net.SplitHostPort(egress.socksAddr); err == nil {
			socksPort = port
		}
	}
	denialTag := ""
	if policy.MonitorDenials {
		denialTag = nextSandboxDenialTag()
	}
	args := []string{"-p", seatbeltProfileFromPermissionProfile(profile, policy, proxyPort, socksPort, denialTag), spec.Name}
	args = append(args, spec.Args...)
	envBackend := backend.Name
	if envBackend == "" {
		envBackend = BackendMacOSSeatbelt
	}
	env := sandboxEnvironment(policy, envBackend, workspaceRoot)
	if egress != nil {
		env = append(env, ProxyEnvWithSocks(egress.addr, egress.socksAddr)...)
		env = appendCACertEnv(env, egress)
	}
	plan := CommandPlan{
		Backend:           backend,
		TargetBackend:     backend.TargetBackend(),
		WorkspaceRoot:     workspaceRoot,
		Policy:            policy,
		Wrapped:           true,
		SandboxEnvMarkers: backend.SandboxEnvMarkers(policy),
		EnforcementLevel:  backend.EnforcementLevel(policy),
		Name:              backend.Executable,
		Args:              args,
		Dir:               spec.Dir,
		Env:               env,
		SandboxDir:        spec.Dir,
	}
	if egress != nil {
		plan.cleanup = egress.cleanup
	}
	// The plan's monitor tag MUST equal the one embedded in the profile above so the
	// monitor matches exactly this run's denials.
	plan.MonitorTag = denialTag
	return plan
}

func seatbeltCompatibilityPermissionProfile(writeRoots []string, policy Policy) PermissionProfile {
	fs := FileSystemPolicy{
		Kind:                 FileSystemUnrestricted,
		ReadRoots:            []string{string(filepath.Separator)},
		IncludePlatformRoots: true,
		AllowTemp:            true,
	}
	if policy.EnforceWorkspace {
		fs.Kind = FileSystemRestricted
		fs.WriteRoots = make([]WritableRoot, 0, len(writeRoots))
		for _, root := range writeRoots {
			fs.WriteRoots = append(fs.WriteRoots, WritableRoot{Root: root})
		}
	}
	fs.DenyRead = normalizeProfilePaths(policy.DenyRead)
	fs.DenyWrite = normalizeProfilePaths(policy.DenyWrite)
	return PermissionProfile{
		FileSystem: fs,
		Network: NetworkPolicy{
			Mode:           effectiveNetwork(policy),
			AllowedDomains: normalizeDomains(policy.AllowedDomains),
			DeniedDomains:  normalizeDomains(policy.DeniedDomains),
			ProxyRequired:  effectiveNetwork(policy) == NetworkScoped,
		},
	}
}

func existingBubblewrapMounts() []string {
	candidates := []string{"/bin", "/usr", "/lib", "/lib64", "/sbin", "/etc"}
	mounts := []string{}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			mounts = append(mounts, candidate)
		}
	}
	return mounts
}

func sandboxEnvironment(policy Policy, backend BackendName, home string) []string {
	env := []string{
		"HOME=" + home,
		"PATH=" + firstEnv("PATH", defaultPath()),
		"TERM=" + firstEnv("TERM", "dumb"),
		EnvSandboxBackend + "=" + string(backend),
		"ZERO_SANDBOX_NETWORK=" + string(policy.Network),
		EnvSandboxed + "=1",
	}
	if runtime.GOOS == "windows" {
		env = append(env, "COMSPEC="+firstEnv("COMSPEC", "cmd.exe"))
	}
	return env
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string{}, values...)
}

func firstEnv(key string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func defaultPath() string {
	if runtime.GOOS == "windows" {
		return os.Getenv("PATH")
	}
	return "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
}

// sandboxWritableDevices are the standard character devices that virtually every
// command needs to write to (e.g. `> /dev/null`). The bubblewrap backend exposes
// these via `--dev /dev`; the sandbox-exec profile must allow them explicitly or
// the equivalent operations fail with "Operation not permitted".
var sandboxWritableDevices = []string{
	"/dev/null",
	"/dev/zero",
	"/dev/random",
	"/dev/urandom",
	"/dev/stdin",
	"/dev/stdout",
	"/dev/stderr",
	"/dev/tty",
	"/dev/dtracehelper",
}

// sandboxWritableSubpaths are non-workspace trees the sandbox-exec profile must
// keep writable for parity with the bubblewrap backend's writable /tmp tmpfs.
// macOS resolves /tmp and /var to their /private counterparts before the sandbox
// check, so both forms are listed. /dev/fd covers process-substitution writes.
var sandboxWritableSubpaths = []string{
	"/tmp",
	"/private/tmp",
	"/var/tmp",
	"/private/var/tmp",
	"/var/folders",
	"/private/var/folders",
	"/dev/fd",
}

// sandboxMachServices is the curated allowlist of Mach services a sandboxed
// command may look up. Under the seatbelt default-deny, XPC to common system
// daemons is otherwise blocked, so tools that touch the keychain
// (securityd/trustd), user/group lookup (opendirectoryd), preferences
// (cfprefsd), network config (SystemConfiguration), launch services, or the
// pasteboard fail. None of these grant filesystem or network access — those stay
// governed by the file-write and network rules below — so the workspace boundary
// is unaffected.
var sandboxMachServices = []string{
	"com.apple.system.opendirectoryd.libinfo",
	"com.apple.system.opendirectoryd.membership",
	"com.apple.system.opendirectoryd.api",
	"com.apple.system.logger",
	"com.apple.logd",
	"com.apple.cfprefsd.daemon",
	"com.apple.cfprefsd.agent",
	"com.apple.securityd",
	"com.apple.securityd.xpc",
	"com.apple.SecurityServer",
	"com.apple.trustd",
	"com.apple.trustd.agent",
	"com.apple.SystemConfiguration.configd",
	"com.apple.SystemConfiguration.DNSConfiguration",
	"com.apple.lsd.mapdb",
	"com.apple.coreservices.launchservicesd",
	"com.apple.pasteboard.1",
}

// sandboxDenialLogTag is the base marker for a sandbox-exec denial in the unified
// log when Policy.MonitorDenials is set; nextSandboxDenialTag derives a unique
// per-plan tag from it so the runtime monitor can find this run's denials via
// `log stream`.
const sandboxDenialLogTag = "zero-sandbox-denied-v1"

// sandboxDenialTagSeq makes each monitored plan's denial tag unique.
var sandboxDenialTagSeq atomic.Uint64

// nextSandboxDenialTag returns a process-unique denial tag. Without uniqueness,
// two concurrent monitored commands share one marker and StartDenialMonitor —
// which filters `log stream` only by tag — would ingest each other's denials,
// leaking unrelated paths/hosts into the wrong <sandbox_violations> block. The pid
// disambiguates across processes; the counter across plans within a process.
func nextSandboxDenialTag() string {
	return fmt.Sprintf("%s-%d-%d", sandboxDenialLogTag, os.Getpid(), sandboxDenialTagSeq.Add(1))
}

func sandboxMachLookupRule() string {
	filters := make([]string, 0, len(sandboxMachServices))
	for _, service := range sandboxMachServices {
		filters = append(filters, `(global-name "`+sandboxProfileString(service)+`")`)
	}
	return "(allow mach-lookup\n  " + strings.Join(filters, "\n  ") + ")"
}

func sandboxExecProfile(writeRoots []string, policy Policy, proxyPort string, socksPort string, denialTag string) string {
	return seatbeltProfileFromPermissionProfile(seatbeltCompatibilityPermissionProfile(writeRoots, policy), policy, proxyPort, socksPort, denialTag)
}

func seatbeltProfileFromPermissionProfile(profile PermissionProfile, policy Policy, proxyPort string, socksPort string, denialTag string) string {
	networkRule := networkRuleForProfile(profile.Network, proxyPort, socksPort)
	readRule := seatbeltReadRule(profile.FileSystem)
	writeRule := seatbeltWriteRule(profile.FileSystem)
	denyDefault := "(deny default)"
	if denialTag != "" {
		// Tag denials so the runtime log monitor can attribute them to THIS run; the
		// message is emitted to the unified log on every deny and StartDenialMonitor
		// filters `log stream` for this exact (per-plan) tag.
		denyDefault = `(deny default (with message "` + sandboxProfileString(denialTag) + `"))`
	}
	rules := []string{
		"(version 1)",
		denyDefault,
		"(allow process*)",
		"(allow process-info* (target same-sandbox))",
		"(allow sysctl-read)",
		"(allow sysctl-write (sysctl-name \"kern.grade_cputype\"))",
		"(allow iokit-open (iokit-registry-entry-class \"RootDomainUserClient\"))",
		"(allow ipc-posix-sem)",
		`(allow ipc-posix-shm-read-data ipc-posix-shm-write-create ipc-posix-shm-write-unlink (ipc-posix-name-regex #"^/__KMP_REGISTERED_LIB_[0-9]+$"))`,
		"(allow pseudo-tty)",
		`(allow file-read* file-write* file-ioctl (literal "/dev/ptmx"))`,
		`(allow file-read* file-write* (require-all (regex #"^/dev/ttys[0-9]+") (extension "com.apple.sandbox.pty")))`,
		`(allow file-ioctl (regex #"^/dev/ttys[0-9]+"))`,
		"(allow ipc-posix-shm-read* (ipc-posix-name-prefix \"apple.cfprefs.\"))",
		"(allow user-preference-read)",
		// Let a sandboxed command signal itself and its own process group so scripts
		// that spawn and kill children (e.g. `sleep 30 & kill %1`, test runners,
		// timeouts) work. The target restriction keeps it from signalling any
		// process outside its own group.
		"(allow signal (target self) (target pgrp))",
		sandboxMachLookupRule(),
		seatbeltPlatformRuntimeRules(),
		readRule,
		writeRule,
	}
	rules = append(rules, denyReadRules(profile.FileSystem)...)
	rules = append(rules, writeRootCarveoutDenyRules(profile.FileSystem)...)
	rules = append(rules, denyWriteRulesFromPaths(profile.FileSystem.DenyWrite)...)
	rules = append(rules, networkRule)
	return strings.Join(nonEmptyStrings(rules), "\n")
}

func seatbeltReadRule(fs FileSystemPolicy) string {
	if fs.Kind == FileSystemUnrestricted {
		return "(allow file-read*)"
	}
	filters := make([]string, 0, len(fs.ReadRoots)+len(macosPlatformReadRoots()))
	for _, root := range fs.ReadRoots {
		filters = appendSeatbeltSubpathFilter(filters, root)
	}
	if fs.IncludePlatformRoots {
		for _, root := range macosPlatformReadRoots() {
			filters = appendSeatbeltSubpathFilter(filters, root)
		}
	}
	if len(filters) == 0 {
		return ""
	}
	return "(allow file-read* file-test-existence\n  " + strings.Join(filters, "\n  ") + ")"
}

func seatbeltWriteRule(fs FileSystemPolicy) string {
	if fs.Kind == FileSystemUnrestricted {
		return "(allow file-write*)"
	}
	filters := make([]string, 0, len(fs.WriteRoots)+len(sandboxWritableSubpaths)+len(sandboxWritableDevices))
	for _, root := range fs.WriteRoots {
		if filter := seatbeltWritableRootFilter(root); filter != "" {
			filters = append(filters, filter)
		}
	}
	if fs.AllowTemp {
		for _, subpath := range sandboxWritableSubpaths {
			filters = append(filters, `(subpath "`+sandboxProfileString(subpath)+`")`)
		}
	}
	for _, device := range sandboxWritableDevices {
		filters = append(filters, `(literal "`+sandboxProfileString(device)+`")`)
	}
	if len(filters) == 0 {
		return ""
	}
	return "(allow file-write*\n  " + strings.Join(filters, "\n  ") + ")"
}

func seatbeltWritableRootFilter(root WritableRoot) string {
	rootPath := strings.TrimSpace(root.Root)
	if rootPath == "" {
		return ""
	}
	return `(subpath "` + sandboxProfileString(rootPath) + `")`
}

func seatbeltProtectedMetadataRegex(root string, name string) string {
	root = strings.TrimSpace(filepath.ToSlash(filepath.Clean(root)))
	name = strings.Trim(strings.TrimSpace(name), `/\`)
	if root == "" || name == "" || name == "." {
		return ""
	}
	root = strings.TrimRight(root, "/")
	if root == "" {
		root = "/"
	}
	escapedRoot := regexpQuoteMeta(root)
	escapedName := regexpQuoteMeta(name)
	if root == "/" {
		return "^/" + escapedName + "(/.*)?$"
	}
	return "^" + escapedRoot + "/" + escapedName + "(/.*)?$"
}

func denyReadRules(fs FileSystemPolicy) []string {
	return denySeatbeltPathRules("file-read*", fs.DenyRead)
}

func writeRootCarveoutDenyRules(fs FileSystemPolicy) []string {
	if fs.Kind != FileSystemRestricted {
		return nil
	}
	var out []string
	for _, root := range fs.WriteRoots {
		for _, subpath := range root.ReadOnlySubpaths {
			subpath = strings.TrimSpace(subpath)
			if subpath == "" {
				continue
			}
			escaped := sandboxProfileString(subpath)
			out = append(out,
				`(deny file-write* (literal "`+escaped+`"))`,
				`(deny file-write* (subpath "`+escaped+`"))`,
			)
		}
		for _, name := range root.ProtectedMetadataNames {
			regex := seatbeltProtectedMetadataRegex(root.Root, name)
			if regex == "" {
				continue
			}
			out = append(out, `(deny file-write* (regex #"`+sandboxProfileRegex(regex)+`"))`)
		}
	}
	return out
}

// denyWriteRules returns seatbelt deny clauses for the policy's resolved
// DenyWrite paths: a (subpath ...) clause for a directory, a (literal ...) clause
// for a single file. Empty when DenyWrite is unset.
func denyWriteRules(policy Policy) []string {
	return denyWriteRulesFromPaths(resolvePolicyPaths(policy.DenyWrite))
}

func denyWriteRulesFromPaths(paths []string) []string {
	return denySeatbeltPathRules("file-write*", paths)
}

func denySeatbeltPathRules(action string, paths []string) []string {
	resolved := normalizeProfilePaths(paths)
	if len(resolved) == 0 {
		return nil
	}
	out := make([]string, 0, len(resolved)*2)
	for _, path := range resolved {
		filters := []string{`(subpath "` + sandboxProfileString(path) + `")`}
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			filters = []string{`(literal "` + sandboxProfileString(path) + `")`}
		} else {
			filters = append(filters, `(literal "`+sandboxProfileString(path)+`")`)
		}
		for _, filter := range filters {
			out = append(out, "(deny "+action+" "+filter+")")
			if action == "file-read*" {
				out = append(out, "(deny file-write-unlink "+filter+")")
			}
		}
	}
	return out
}

// networkRuleFor returns the seatbelt network clause for a policy. allow opens
// all network; deny (and an empty-allowlist scoped policy, which effectiveNetwork
// collapses to deny) blocks all network; scoped denies general network but
// permits only outbound to the local proxy ports on localhost, so traffic must
// flow through the filtering proxy. Both the HTTP proxy port and the SOCKS5
// front-end port are allowed (a sandboxed process configured with
// ALL_PROXY=socks5://... must reach the SOCKS listener). A scoped policy with no
// resolvable proxy port falls back to a full deny (fail closed).
func networkRuleFor(policy Policy, proxyPort string, socksPort string) string {
	return networkRuleForProfile(NetworkPolicy{Mode: effectiveNetwork(policy)}, proxyPort, socksPort)
}

func networkRuleForProfile(network NetworkPolicy, proxyPort string, socksPort string) string {
	switch network.Mode {
	case NetworkAllow:
		return "(allow network*)"
	case NetworkScoped:
		rules := []string{"(deny network*)"}
		seen := map[string]struct{}{}
		// Deny by default, then allow only outbound to each distinct proxy port on
		// loopback. Duplicate/empty ports are skipped so the clause stays minimal.
		for _, port := range []string{proxyPort, socksPort} {
			port = strings.TrimSpace(port)
			if port == "" {
				continue
			}
			if _, ok := seen[port]; ok {
				continue
			}
			seen[port] = struct{}{}
			rules = append(rules, `(allow network-outbound (remote ip "localhost:`+sandboxProfileString(port)+`"))`)
		}
		if len(rules) == 1 {
			// No resolvable proxy port: fail closed.
			return "(deny network*)"
		}
		return strings.Join(rules, "\n")
	default:
		return "(deny network*)"
	}
}

func sandboxProfileString(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`)
	return replacer.Replace(value)
}

func sandboxProfileRegex(value string) string {
	replacer := strings.NewReplacer(`"`, `\"`, "\n", `\n`, "\r", `\r`)
	return replacer.Replace(value)
}

func appendSeatbeltSubpathFilter(filters []string, root string) []string {
	root = strings.TrimSpace(root)
	if root == "" {
		return filters
	}
	return append(filters, `(subpath "`+sandboxProfileString(root)+`")`)
}

func macosPlatformReadRoots() []string {
	return []string{
		"/bin",
		"/sbin",
		"/Applications",
		"/Library/Apple/System/Library/Frameworks",
		"/Library/Apple/System/Library/PrivateFrameworks",
		"/Library/Apple/usr/lib",
		"/usr/bin",
		"/usr/sbin",
		"/usr/lib",
		"/usr/libexec",
		"/usr/share",
		"/usr/local/lib",
		"/opt/homebrew/lib",
		"/etc",
		"/private/etc",
		"/var/db",
		"/private/var/db",
		"/System/Library",
		"/System/iOSSupport/System/Library/Frameworks",
		"/System/iOSSupport/System/Library/PrivateFrameworks",
		"/System/iOSSupport/System/Library/SubFrameworks",
		"/Library/Apple",
		"/Library/Preferences",
		"/dev",
	}
}

func seatbeltPlatformRuntimeRules() string {
	return strings.Join([]string{
		`(allow file-map-executable`,
		`  (subpath "/Library/Apple/System/Library/Frameworks")`,
		`  (subpath "/Library/Apple/System/Library/PrivateFrameworks")`,
		`  (subpath "/Library/Apple/usr/lib")`,
		`  (subpath "/System/Library/Extensions")`,
		`  (subpath "/System/Library/Frameworks")`,
		`  (subpath "/System/Library/PrivateFrameworks")`,
		`  (subpath "/System/Library/SubFrameworks")`,
		`  (subpath "/System/iOSSupport/System/Library/Frameworks")`,
		`  (subpath "/System/iOSSupport/System/Library/PrivateFrameworks")`,
		`  (subpath "/System/iOSSupport/System/Library/SubFrameworks")`,
		`  (subpath "/usr/lib"))`,
		`(allow system-mac-syscall (mac-policy-name "vnguard"))`,
		`(allow system-mac-syscall (require-all (mac-policy-name "Sandbox") (mac-syscall-number 67)))`,
		`(allow file-read-metadata file-test-existence`,
		`  (literal "/etc")`,
		`  (literal "/tmp")`,
		`  (literal "/var")`,
		`  (literal "/private/etc/localtime"))`,
		`(allow file-read-metadata file-test-existence (path-ancestors "/System/Volumes/Data/private"))`,
		`(allow file-read* file-test-existence (literal "/"))`,
		`(allow system-fsctl (fsctl-command FSIOC_CAS_BSDFLAGS))`,
		`(allow file-read* file-test-existence`,
		`  (literal "/dev/autofs_nowait")`,
		`  (literal "/dev/random")`,
		`  (literal "/dev/urandom")`,
		`  (literal "/private/etc/master.passwd")`,
		`  (literal "/private/etc/passwd")`,
		`  (literal "/private/etc/protocols")`,
		`  (literal "/private/etc/services"))`,
		`(allow file-read* file-test-existence file-write-data`,
		`  (literal "/dev/null")`,
		`  (literal "/dev/zero"))`,
		`(allow file-read-data file-test-existence file-write-data (subpath "/dev/fd"))`,
		`(allow file-read* file-test-existence file-write-data file-ioctl (literal "/dev/dtracehelper"))`,
		`(allow file-read* file-test-existence file-write* (subpath "/tmp"))`,
		`(allow file-read* file-write* (subpath "/private/tmp"))`,
		`(allow file-read* file-write* (subpath "/var/tmp"))`,
		`(allow file-read* file-write* (subpath "/private/var/tmp"))`,
		`(allow file-read* file-test-existence`,
		`  (literal "/System/Library/CoreServices")`,
		`  (literal "/System/Library/CoreServices/.SystemVersionPlatform.plist")`,
		`  (literal "/System/Library/CoreServices/SystemVersion.plist"))`,
		`(allow file-read-metadata (subpath "/var"))`,
		`(allow file-read-metadata (subpath "/private/var"))`,
		`(allow network-outbound (literal "/private/var/run/syslog"))`,
		`(allow ipc-posix-shm-read* (ipc-posix-name "apple.shm.notification_center"))`,
		`(allow file-read* (literal "/private/var/db/eligibilityd/eligibility.plist"))`,
		`(allow file-read* (extension "com.apple.app-sandbox.read"))`,
		`(allow file-read* file-write* (extension "com.apple.app-sandbox.read-write"))`,
	}, "\n")
}

func nonEmptyStrings(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

func regexpQuoteMeta(value string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		`.`, `\.`,
		`+`, `\+`,
		`*`, `\*`,
		`?`, `\?`,
		`(`, `\(`,
		`)`, `\)`,
		`|`, `\|`,
		`[`, `\[`,
		`]`, `\]`,
		`{`, `\{`,
		`}`, `\}`,
		`^`, `\^`,
		`$`, `\$`,
	)
	return replacer.Replace(value)
}
