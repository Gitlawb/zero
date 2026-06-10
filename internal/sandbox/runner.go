package sandbox

import (
	"context"
	"errors"
	"fmt"
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
// the workspace root plus any granted extra roots.
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
			return bubblewrapCommandPlan(spec, workspaceRoot, relativeDir, engine.writeRoots(workspaceRoot), policy, backend), nil
		}
	case BackendSandboxExec:
		if backend.Available && backend.Executable != "" {
			return sandboxExecCommandPlan(spec, workspaceRoot, engine.writeRoots(workspaceRoot), policy, backend), nil
		}
	}
	if !policy.AllowPolicyOnlyRunner {
		return CommandPlan{}, errPolicyOnlyRunnerDisabled
	}
	return directCommandPlan(spec, backend, policy, workspaceRoot), nil
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
		if root == workspaceRoot {
			continue
		}
		args = append(args, "--bind", root, root)
	}
	args = append(args, "--chdir", sandboxDir)
	if policy.Network == NetworkDeny {
		args = append(args, "--unshare-net")
	}
	for _, mount := range existingBubblewrapMounts() {
		args = append(args, "--ro-bind", mount, mount)
	}
	args = append(args, "--clearenv")
	for _, env := range sandboxEnvironment(policy, BackendBubblewrap, bubblewrapWorkspace) {
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

func sandboxExecCommandPlan(spec CommandSpec, workspaceRoot string, writeRoots []string, policy Policy, backend Backend) CommandPlan {
	args := []string{"-p", sandboxExecProfile(writeRoots, policy), spec.Name}
	args = append(args, spec.Args...)
	return CommandPlan{
		Backend:       backend,
		WorkspaceRoot: workspaceRoot,
		Policy:        policy,
		Wrapped:       true,
		Name:          backend.Executable,
		Args:          args,
		Dir:           spec.Dir,
		Env:           sandboxEnvironment(policy, BackendSandboxExec, workspaceRoot),
		SandboxDir:    spec.Dir,
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

func sandboxExecProfile(writeRoots []string, policy Policy) string {
	networkRule := "(deny network*)"
	if policy.Network == NetworkAllow {
		networkRule = "(allow network*)"
	}
	writeRule := "(allow file-write*)"
	if policy.EnforceWorkspace {
		subpaths := make([]string, 0, len(writeRoots))
		for _, root := range writeRoots {
			subpaths = append(subpaths, `(subpath "`+sandboxProfileString(root)+`")`)
		}
		writeRule = "(allow file-write* " + strings.Join(subpaths, " ") + ")"
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

func sandboxProfileString(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`)
	return replacer.Replace(value)
}
