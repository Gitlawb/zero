package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/Gitlawb/zero/internal/config"
	zeroSandbox "github.com/Gitlawb/zero/internal/sandbox"
)

// runSandboxExec runs ONE command through the real sandbox and exits with its
// status.
//
// This exists because until now the sandbox could only be exercised through a
// full agent turn with a model in the loop. `zero sandbox policy` reports what
// the posture would be and `zero sandbox check` evaluates a hypothetical
// decision, but nothing actually ran a command and let you look at what
// happened on disk afterwards. The practical result is that enforcement is
// covered almost entirely by tests asserting the shape of an ACL plan, and
// almost not at all by tests asserting a write was refused.
//
// A plan can be perfectly correct and never reach the filesystem. That is not
// hypothetical here: the .git rename guard was emitted correctly by the planner
// and silently skipped by the applier, and four tests covering the plan all
// passed while the ACE was absent from disk. Something that runs the real
// binary and then stats the file is the only thing that catches that class.
//
// Deliberately NOT a debug curiosity: it takes the same path a shell tool
// takes, through SandboxManager.BuildCommandPlan, so what it proves is what
// users get. It prints the resolved backend and enforcement level to stderr
// before running, so a harness can assert the sandbox was actually engaged
// rather than quietly downgraded.
func runSandboxExec(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	command, err := parseSandboxExecArgs(args)
	if err != nil {
		if errors.Is(err, errSandboxExecHelp) {
			if writeErr := writeSandboxExecHelp(stdout); writeErr != nil {
				return exitCrash
			}
			return exitSuccess
		}
		return writeExecUsageError(stderr, err.Error())
	}

	workspaceRoot, err := resolveWorkspaceRoot("", deps)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	resolved, err := deps.resolveConfig(workspaceRoot, config.Overrides{})
	if err != nil {
		return writeAppError(stderr, err.Error(), exitProvider)
	}
	policy := applyConfiguredSandboxPolicy(zeroSandbox.DefaultPolicy(), resolved.Sandbox)

	scope, err := zeroSandbox.NewScope(workspaceRoot, resolved.Sandbox.AdditionalWriteRoots)
	if err != nil {
		return writeAppError(stderr, fmt.Sprintf("resolve sandbox write roots: %v", err), exitCrash)
	}

	manager := zeroSandbox.NewSandboxManager(zeroSandbox.SandboxManagerOptions{
		Backend: deps.selectSandboxBackend(zeroSandbox.BackendOptions{}),
	})
	plan, err := manager.BuildCommandPlan(zeroSandbox.SandboxManagerRequest{
		WorkspaceRoot: workspaceRoot,
		Command: zeroSandbox.CommandSpec{
			Name: command[0],
			Args: command[1:],
			Dir:  workspaceRoot,
			Env:  os.Environ(),
		},
		Policy: policy,
		Scope:  scope,
		// Ask for validation rather than a best-effort plan: a harness asserting
		// that a write was refused needs to know the sandbox was really there.
		ValidateExecution: true,
	})
	if err != nil {
		return writeAppError(stderr, fmt.Sprintf("build sandbox command plan: %v", err), exitCrash)
	}

	// Printed before the command runs and on stderr, so it survives a command
	// that writes to stdout and stays greppable by a test harness. A downgrade
	// is reported loudly for the same reason: a smoke test that passes because
	// the sandbox quietly stood down is worse than no smoke test.
	fmt.Fprintf(stderr, "sandbox: backend=%s enforcement=%s wrapped=%t workspace=%s\n",
		plan.Backend.Name, plan.EnforcementLevel, plan.Wrapped, plan.WorkspaceRoot)
	if strings.TrimSpace(plan.DowngradeReason) != "" {
		fmt.Fprintf(stderr, "sandbox: DOWNGRADED: %s\n", plan.DowngradeReason)
	}

	return runSandboxPlannedCommand(plan, stdout, stderr)
}

func runSandboxPlannedCommand(plan zeroSandbox.CommandPlan, stdout io.Writer, stderr io.Writer) int {
	process := exec.Command(plan.Name, plan.Args...)
	process.Dir = plan.Dir
	if process.Dir == "" {
		process.Dir = plan.WorkspaceRoot
	}
	if len(plan.Env) > 0 {
		process.Env = plan.Env
	}
	process.Stdin = os.Stdin
	process.Stdout = stdout
	process.Stderr = stderr

	if err := process.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// The command's own status, not ours. A harness asserting "the write
			// was refused" needs the refusal's exit code, not a wrapper's.
			return exitErr.ExitCode()
		}
		fmt.Fprintf(stderr, "sandbox exec: %v\n", err)
		return exitCrash
	}
	return exitSuccess
}

var errSandboxExecHelp = errors.New("help requested")

// parseSandboxExecArgs takes everything after `--` as the command, so the
// command's own flags are never mistaken for ours.
func parseSandboxExecArgs(args []string) ([]string, error) {
	for index, arg := range args {
		switch arg {
		case "-h", "--help", "help":
			return nil, errSandboxExecHelp
		case "--":
			command := args[index+1:]
			if len(command) == 0 {
				return nil, errors.New("usage: zero sandbox exec -- <command> [args...]")
			}
			return command, nil
		}
	}
	if len(args) == 0 {
		return nil, errors.New("usage: zero sandbox exec -- <command> [args...]")
	}
	// Tolerated without the separator for interactive use, but the separator is
	// what the help shows, because anything with a leading dash needs it.
	return args, nil
}

func writeSandboxExecHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero sandbox exec -- <command> [args...]

Runs one command through the real sandbox and exits with its status.

Everything after the -- separator is the command, so its own flags are not
parsed as Zero's. The resolved backend and enforcement level are written to
stderr before the command runs, and a downgrade is reported there explicitly.

Examples:
  zero sandbox exec -- cmd /c echo hello
  zero sandbox exec -- powershell -Command "Set-Content out.txt x"

`)
	return err
}
