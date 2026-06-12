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

// scopedProxyEnv returns the proxy environment variables that route a sandboxed
// process's HTTP(S) traffic through the local filtering proxy at addr. Both
// upper- and lower-case forms are set because different clients read different
// casings. localhost is excluded so loopback (the proxy itself) is reached
// directly.
func scopedProxyEnv(addr string) []string {
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
	if engine.scope != nil {
		return engine.scope.Roots()
	}
	return []string{workspaceRoot}
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
	addr    string
	cleanup func()
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
	return &scopedEgress{addr: proxy.Addr(), cleanup: func() { _ = proxy.Close() }}, nil
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
	args = append(args, "--", spec.Name)
	args = append(args, spec.Args...)
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

func sandboxExecCommandPlan(spec CommandSpec, workspaceRoot string, writeRoots []string, policy Policy, backend Backend, egress *scopedEgress) CommandPlan {
	var proxyPort string
	if egress != nil {
		if _, port, err := net.SplitHostPort(egress.addr); err == nil {
			proxyPort = port
		}
	}
	args := []string{"-p", sandboxExecProfile(writeRoots, policy, proxyPort), spec.Name}
	args = append(args, spec.Args...)
	env := sandboxEnvironment(policy, BackendSandboxExec, workspaceRoot)
	if egress != nil {
		env = append(env, scopedProxyEnv(egress.addr)...)
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
		"ZERO_SANDBOX_BACKEND=" + string(backend),
		"ZERO_SANDBOX_NETWORK=" + string(policy.Network),
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

func sandboxExecProfile(writeRoots []string, policy Policy, proxyPort string) string {
	networkRule := networkRuleFor(policy, proxyPort)
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
	return strings.Join([]string{
		"(version 1)",
		"(deny default)",
		"(allow process*)",
		"(allow sysctl-read)",
		"(allow file-read*)",
		writeRule,
		networkRule,
	}, "\n")
}

// networkRuleFor returns the seatbelt network clause for a policy. allow opens
// all network; deny (and an empty-allowlist scoped policy, which effectiveNetwork
// collapses to deny) blocks all network; scoped denies general network but
// permits only outbound to the local proxy port on localhost, so traffic must
// flow through the filtering proxy. A scoped policy with no resolvable proxy port
// falls back to a full deny (fail closed).
func networkRuleFor(policy Policy, proxyPort string) string {
	switch effectiveNetwork(policy) {
	case NetworkAllow:
		return "(allow network*)"
	case NetworkScoped:
		if strings.TrimSpace(proxyPort) == "" {
			return "(deny network*)"
		}
		// Deny by default, then allow only outbound to the proxy on loopback.
		return "(deny network*)\n" +
			`(allow network-outbound (remote ip "localhost:` + sandboxProfileString(proxyPort) + `"))`
	default:
		return "(deny network*)"
	}
}

func sandboxProfileString(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`)
	return replacer.Replace(value)
}
