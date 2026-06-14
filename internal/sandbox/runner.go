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

const bubblewrapWorkspace = "/workspace"

var errPolicyOnlyRunnerDisabled = errors.New("policy-only sandbox runner is disabled")

type CommandSpec struct {
	Name string
	Args []string
	Dir  string
	Env  []string
}

type CommandPlan struct {
	Backend       Backend  `json:"backend"`
	WorkspaceRoot string   `json:"workspaceRoot"`
	Policy        Policy   `json:"policy"`
	Wrapped       bool     `json:"wrapped"`
	Name          string   `json:"name"`
	Args          []string `json:"args"`
	Dir           string   `json:"dir,omitempty"`
	Env           []string `json:"env,omitempty"`
	SandboxDir    string   `json:"sandboxDir,omitempty"`
	// MonitorTag, when non-empty, is the unique marker embedded in the
	// sandbox-exec profile's denial messages; a caller passes it to
	// StartDenialMonitor to capture what the sandbox blocked. Empty unless
	// Policy.MonitorDenials is set on a macOS sandbox-exec plan.
	MonitorTag string `json:"monitorTag,omitempty"`
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
	if extra := resolveWriteRootPaths(engine.policy.AllowWrite); len(extra) > 0 {
		roots = dedupeStrings(append(roots, extra...))
	}
	return roots
}

func (engine *Engine) BuildCommandPlan(spec CommandSpec) (CommandPlan, error) {
	if engine == nil {
		return directCommandPlan(spec, Backend{Name: BackendPolicyOnly, Message: "sandbox disabled"}, Policy{}, ""), nil
	}
	policy := engine.policy
	if policy.Mode == "" {
		policy = DefaultPolicy()
	}
	workspaceRoot, commandDir, relativeDir, err := engine.resolveCommandDir(spec.Dir, policy)
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
	// Re-entrancy guard: a command spawned by a process we already wrapped (both
	// ZERO_SANDBOXED=1 and ZERO_SANDBOX_BACKEND set in its env — see
	// IsAlreadySandboxed) must not be wrapped again — nested bwrap / sandbox-exec
	// fails and a second egress proxy would be redundant. Return a pass-through
	// plan. Mirrors the already-sandboxed guard used by comparable executor
	// sandboxes.
	if IsAlreadySandboxed() {
		return directCommandPlan(spec, backend, policy, workspaceRoot), nil
	}
	if policy.Mode == ModeDisabled {
		return directCommandPlan(spec, backend, policy, workspaceRoot), nil
	}
	switch backend.Name {
	case BackendBubblewrap:
		if backend.Available && backend.Executable != "" {
			// Bubblewrap isolates the network namespace (--unshare-net) with no
			// bridge to the host loopback proxy, so it cannot enforce scoped egress;
			// a scoped policy collapses to deny (no proxy is started). Evaluate's
			// network gate denies network-risk tools for this backend to match.
			return bubblewrapCommandPlan(spec, workspaceRoot, relativeDir, engine.writeRoots(workspaceRoot), policy, backend), nil
		}
	case BackendSandboxExec:
		if backend.Available && backend.Executable != "" {
			egress, err := startScopedEgress(policy, backend)
			if err != nil {
				return CommandPlan{}, err
			}
			return sandboxExecCommandPlan(spec, workspaceRoot, engine.writeRoots(workspaceRoot), policy, backend, egress), nil
		}
	}
	if !policy.AllowPolicyOnlyRunner {
		return CommandPlan{}, errPolicyOnlyRunnerDisabled
	}
	return directCommandPlan(spec, backend, policy, workspaceRoot), nil
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
	cleanup   func()
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
		Allowed: policy.AllowedDomains,
		Denied:  policy.DeniedDomains,
	})
	if err != nil {
		return nil, fmt.Errorf("scoped egress unavailable, denying network: %w", err)
	}
	return &scopedEgress{addr: proxy.Addr(), socksAddr: proxy.SocksAddr(), cleanup: func() { _ = proxy.Close() }}, nil
}

func directCommandPlan(spec CommandSpec, backend Backend, policy Policy, workspaceRoot string) CommandPlan {
	return CommandPlan{
		Backend:       backend,
		WorkspaceRoot: workspaceRoot,
		Policy:        policy,
		Wrapped:       false,
		Name:          spec.Name,
		Args:          cloneStrings(spec.Args),
		Dir:           spec.Dir,
		Env:           cloneStrings(spec.Env),
	}
}

func (engine *Engine) resolveCommandDir(dir string, policy Policy) (string, string, string, error) {
	workspaceRoot := strings.TrimSpace(engine.workspaceRoot)
	if workspaceRoot == "" {
		return "", "", "", errors.New("sandbox workspace root is required")
	}
	workspaceRoot = filepath.Clean(workspaceRoot)
	if !filepath.IsAbs(workspaceRoot) {
		absolute, err := filepath.Abs(workspaceRoot)
		if err != nil {
			return "", "", "", fmt.Errorf("resolve sandbox workspace: %w", err)
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
			return "", "", "", Violation{
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
	relativeDir, err := filepath.Rel(workspaceRoot, commandDir)
	if err != nil {
		return "", "", "", fmt.Errorf("resolve sandbox command directory: %w", err)
	}
	if relativeDir == "." {
		relativeDir = ""
	}
	return workspaceRoot, commandDir, relativeDir, nil
}

func bubblewrapCommandPlan(spec CommandSpec, workspaceRoot string, relativeDir string, writeRoots []string, policy Policy, backend Backend) CommandPlan {
	sandboxDir := bubblewrapWorkspace
	if relativeDir != "" {
		sandboxDir = filepath.ToSlash(filepath.Join(bubblewrapWorkspace, relativeDir))
	}
	// A cwd inside an extra write root is outside the /workspace remount; the
	// extra root is bound at its real host path, so chdir there directly.
	// (resolveCommandDir has already validated the cwd against the scope when
	// EnforceWorkspace is on; an unvalidated out-of-scope cwd just makes
	// bwrap's chdir fail closed.)
	if relativeDir == ".." || strings.HasPrefix(relativeDir, ".."+string(filepath.Separator)) {
		sandboxDir = filepath.ToSlash(spec.Dir)
	}
	args := []string{
		"--die-with-parent",
		"--unshare-pid",
		"--unshare-ipc",
		"--unshare-uts",
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
		"--bind", workspaceRoot, bubblewrapWorkspace,
	}
	for _, root := range writeRoots {
		// writeRoots[0] is the scope's workspace root; it is normalized by the
		// same Abs+EvalSymlinks pipeline resolveCommandDir applies to the
		// workspaceRoot parameter, so this equality reliably skips the workspace
		// (already remounted at /workspace) rather than double-binding it.
		if root == workspaceRoot {
			continue
		}
		args = append(args, "--bind", root, root)
	}
	args = append(args, "--chdir", sandboxDir)
	// Both deny and scoped isolate the network namespace with --unshare-net (no raw
	// host network). Scoped does NOT get the filtering proxy here: bubblewrap has no
	// bridge from its netns to the host loopback proxy, so scoped collapses to deny
	// until a real relay (e.g. slirp4netns) is added. allow shares the host network.
	if network := effectiveNetwork(policy); network == NetworkDeny || network == NetworkScoped {
		args = append(args, "--unshare-net")
	}
	for _, mount := range existingBubblewrapMounts() {
		args = append(args, "--ro-bind", mount, mount)
	}
	args = append(args, "--clearenv")
	setenvLines := sandboxEnvironment(policy, BackendBubblewrap, bubblewrapWorkspace)
	for _, env := range setenvLines {
		key, value, ok := strings.Cut(env, "=")
		if ok {
			args = append(args, "--setenv", key, value)
		}
	}
	// Optionally install the seccomp Unix-socket filter by prefixing the command
	// with the zero-seccomp helper, bound read-only into the sandbox at its host
	// path. Off by default; if the helper is not found the command runs without
	// the filter (bubblewrap's filesystem/network isolation still applies).
	command := append([]string{spec.Name}, spec.Args...)
	if policy.BlockUnixSockets {
		if helper := seccompHelper(); helper != "" {
			args = append(args, "--ro-bind", helper, helper)
			command = append([]string{helper}, command...)
		}
	}
	args = append(args, "--")
	args = append(args, command...)
	return CommandPlan{
		Backend:       backend,
		WorkspaceRoot: workspaceRoot,
		Policy:        policy,
		Wrapped:       true,
		Name:          backend.Executable,
		Args:          args,
		SandboxDir:    sandboxDir,
	}
}

// seccompHelper resolves the zero-seccomp wrapper used to install the AF_UNIX
// seccomp filter inside the bubblewrap sandbox (Policy.BlockUnixSockets). It is a
// package var so tests can stub discovery; it returns "" when the helper is not
// found, in which case the sandbox runs without the extra filter rather than
// failing the command.
var seccompHelper = findSeccompHelper

// findSeccompHelper looks for the zero-seccomp helper next to the running
// executable first (the expected install layout), then on PATH. It returns ""
// when no helper is available.
func findSeccompHelper() string {
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "zero-seccomp")
		// Require an executable regular file: selecting a present-but-non-executable
		// file would make the sandboxed command fail with EACCES instead of degrading
		// gracefully. (exec.LookPath below applies the same check on PATH.)
		if info, statErr := os.Stat(candidate); statErr == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return candidate
		}
	}
	if path, err := exec.LookPath("zero-seccomp"); err == nil {
		return path
	}
	return ""
}

func sandboxExecCommandPlan(spec CommandSpec, workspaceRoot string, writeRoots []string, policy Policy, backend Backend, egress *scopedEgress) CommandPlan {
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
	args := []string{"-p", sandboxExecProfile(writeRoots, policy, proxyPort, socksPort, denialTag), spec.Name}
	args = append(args, spec.Args...)
	env := sandboxEnvironment(policy, BackendSandboxExec, workspaceRoot)
	if egress != nil {
		env = append(env, ProxyEnvWithSocks(egress.addr, egress.socksAddr)...)
	}
	plan := CommandPlan{
		Backend:       backend,
		WorkspaceRoot: workspaceRoot,
		Policy:        policy,
		Wrapped:       true,
		Name:          backend.Executable,
		Args:          args,
		Dir:           spec.Dir,
		Env:           env,
		SandboxDir:    spec.Dir,
	}
	if egress != nil {
		plan.cleanup = egress.cleanup
	}
	// The plan's monitor tag MUST equal the one embedded in the profile above so the
	// monitor matches exactly this run's denials.
	plan.MonitorTag = denialTag
	return plan
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
	networkRule := networkRuleFor(policy, proxyPort, socksPort)
	writeRule := "(allow file-write*)"
	if policy.EnforceWorkspace {
		// The granted write roots are the only writable *project* locations. Temp
		// trees and the standard device nodes are the only additions, matching what
		// the bubblewrap backend already grants (--tmpfs /tmp, --dev /dev).
		filters := make([]string, 0, len(writeRoots)+len(sandboxWritableSubpaths)+len(sandboxWritableDevices))
		for _, root := range writeRoots {
			filters = append(filters, `(subpath "`+sandboxProfileString(root)+`")`)
		}
		for _, subpath := range sandboxWritableSubpaths {
			filters = append(filters, `(subpath "`+subpath+`")`)
		}
		for _, device := range sandboxWritableDevices {
			filters = append(filters, `(literal "`+device+`")`)
		}
		writeRule = "(allow file-write*\n  " + strings.Join(filters, "\n  ") + ")"
	}
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
		"(allow sysctl-read)",
		// Let a sandboxed command signal itself and its own process group so scripts
		// that spawn and kill children (e.g. `sleep 30 & kill %1`, test runners,
		// timeouts) work. The target restriction keeps it from signalling any
		// process outside its own group.
		"(allow signal (target self) (target pgrp))",
		sandboxMachLookupRule(),
		"(allow file-read*)",
		writeRule,
	}
	// DenyWrite entries are emitted AFTER the write allow so seatbelt's
	// last-match-wins makes them override the workspace/AllowWrite binds — so
	// "DenyWrite wins" holds for shell commands too, not just the policy gate.
	rules = append(rules, denyWriteRules(policy)...)
	rules = append(rules, networkRule)
	return strings.Join(rules, "\n")
}

// denyWriteRules returns seatbelt deny clauses for the policy's resolved
// DenyWrite paths: a (subpath ...) clause for a directory, a (literal ...) clause
// for a single file. Empty when DenyWrite is unset.
func denyWriteRules(policy Policy) []string {
	resolved := resolvePolicyPaths(policy.DenyWrite)
	if len(resolved) == 0 {
		return nil
	}
	out := make([]string, 0, len(resolved))
	for _, path := range resolved {
		filter := `(subpath "` + sandboxProfileString(path) + `")`
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			filter = `(literal "` + sandboxProfileString(path) + `")`
		}
		out = append(out, "(deny file-write* "+filter+")")
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
	switch effectiveNetwork(policy) {
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
