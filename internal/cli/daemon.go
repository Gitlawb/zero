package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Gitlawb/zero/internal/background"
	"github.com/Gitlawb/zero/internal/daemon"
)

// runDaemon dispatches the `zero daemon ...` subcommands. The daemon supervises a
// pool of headless `zero exec` workers and routes sessions to them over an
// owner-only local control socket. It is an ADDITIVE surface — interactive and
// one-shot exec are unchanged.
func runDaemon(args []string, stdout io.Writer, stderr io.Writer, _ appDeps) int {
	if len(args) == 0 {
		return writeDaemonUsage(stderr, exitUsage)
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "start":
		return runDaemonStart(rest, stdout, stderr)
	case "stop":
		return runDaemonStop(rest, stdout, stderr)
	case "status":
		return runDaemonStatus(rest, stdout, stderr)
	case "run":
		return runDaemonRun(rest, stdout, stderr)
	case "attach":
		return runDaemonAttach(rest, stdout, stderr)
	case "-h", "--help", "help":
		return writeDaemonUsage(stdout, exitSuccess)
	default:
		fmt.Fprintf(stderr, "unknown daemon subcommand %q\n", sub)
		return writeDaemonUsage(stderr, exitUsage)
	}
}

func writeDaemonUsage(w io.Writer, code int) int {
	fmt.Fprint(w, `Usage: zero daemon <command>

Commands:
  start [--foreground]      Start the daemon (background by default).
  stop                      Gracefully stop the running daemon.
  status                    Show daemon / pool / session status.
  run --session <id> [--cwd <dir>] [--prompt <text>] [exec flags...]
                            Create/route a session and stream its output.
  attach <session>          Attach to a running session's stream.
`)
	return code
}

// runDaemonStart starts the daemon. Without --foreground it spawns a detached
// background process running the foreground daemon and returns once it is up.
func runDaemonStart(args []string, stdout io.Writer, stderr io.Writer) int {
	foreground := false
	for _, a := range args {
		switch a {
		case "--foreground", "-f":
			foreground = true
		case "-h", "--help":
			return writeDaemonUsage(stdout, exitSuccess)
		default:
			return writeExecUsageError(stderr, fmt.Sprintf("unknown flag %q for daemon start", a))
		}
	}
	paths, err := daemon.DefaultPaths()
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	if foreground {
		return runDaemonForeground(paths, stdout, stderr)
	}
	return runDaemonStartDetached(paths, stdout, stderr)
}

// runDaemonForeground runs the daemon in this process until SIGINT/SIGTERM. Each
// worker is a headless `zero exec` child with the sandbox re-entrancy markers
// scrubbed (NewExecLauncher) so it establishes its own sandbox.
func runDaemonForeground(paths daemon.Paths, stdout io.Writer, stderr io.Writer) int {
	launcher, err := daemon.NewExecLauncher(daemon.ExecLauncherConfig{})
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	logf := func(line string) { fmt.Fprintln(stderr, "[daemon] "+line) }
	pool, err := daemon.NewPool(daemon.PoolOptions{Launcher: launcher, Log: logf})
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	mgr, err := daemon.NewSessionManager(daemon.SessionManagerOptions{Pool: pool})
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	srv, err := daemon.NewServer(daemon.ServerOptions{Paths: paths, Manager: mgr, Pool: pool, Log: logf})
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		srv.Shutdown()
	}()

	if err := srv.Serve(); err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	return exitSuccess
}

// runDaemonStartDetached spawns the foreground daemon as a detached background
// process (its own process group, output to a log file) and waits for it to bind.
func runDaemonStartDetached(paths daemon.Paths, stdout io.Writer, stderr io.Writer) int {
	if daemonReachable(paths) {
		fmt.Fprintln(stdout, "zero daemon is already running")
		return exitSuccess
	}
	exe, err := os.Executable()
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	if err := os.MkdirAll(filepath.Dir(paths.Socket), 0o700); err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	logPath := filepath.Join(filepath.Dir(paths.Socket), "daemon.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	defer logFile.Close()

	cmd := exec.Command(exe, "daemon", "start", "--foreground")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	background.ConfigureChildProcessGroup(cmd) // own process group: outlives this shell
	if err := cmd.Start(); err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	_ = cmd.Process.Release()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if daemonReachable(paths) {
			fmt.Fprintf(stdout, "zero daemon started (socket %s)\n", paths.Socket)
			return exitSuccess
		}
		time.Sleep(25 * time.Millisecond)
	}
	return writeAppError(stderr, "daemon did not come up within timeout; see "+logPath, exitCrash)
}

func runDaemonStop(args []string, stdout io.Writer, stderr io.Writer) int {
	if helpRequested(args) {
		return writeDaemonUsage(stdout, exitSuccess)
	}
	if len(args) > 0 {
		return writeExecUsageError(stderr, "daemon stop takes no arguments")
	}
	paths, err := daemon.DefaultPaths()
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	client, err := daemon.Dial(paths.Socket)
	if err != nil {
		fmt.Fprintln(stdout, "zero daemon is not running")
		return exitSuccess
	}
	defer client.Close()
	if err := client.Shutdown(); err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	fmt.Fprintln(stdout, "zero daemon stopped")
	return exitSuccess
}

func runDaemonStatus(args []string, stdout io.Writer, stderr io.Writer) int {
	if helpRequested(args) {
		return writeDaemonUsage(stdout, exitSuccess)
	}
	if len(args) > 0 {
		return writeExecUsageError(stderr, "daemon status takes no arguments")
	}
	paths, err := daemon.DefaultPaths()
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	client, err := daemon.Dial(paths.Socket)
	if err != nil {
		fmt.Fprintln(stdout, "zero daemon is not running")
		return exitSuccess
	}
	defer client.Close()
	report, err := client.Status()
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	fmt.Fprintf(stdout, "daemon pid=%d version=%d socket=%s\n", report.PID, report.Version, report.Socket)
	fmt.Fprintf(stdout, "pool size=%d busy=%d queue=%d\n", report.PoolSize, len(report.Workers), report.QueueDepth)
	for _, s := range report.Sessions {
		fmt.Fprintf(stdout, "  session %s state=%s lines=%d\n", s.ID, s.State, s.Lines)
	}
	return exitSuccess
}

func runDaemonRun(args []string, stdout io.Writer, stderr io.Writer) int {
	session, cwd, prompt := "", "", ""
	var forward []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		value := func() (string, bool) {
			if i+1 >= len(args) {
				return "", false
			}
			i++
			return args[i], true
		}
		switch {
		case a == "-h" || a == "--help":
			return writeDaemonUsage(stdout, exitSuccess)
		case a == "--session":
			v, ok := value()
			if !ok {
				return writeExecUsageError(stderr, "--session requires a value")
			}
			session = v
		case strings.HasPrefix(a, "--session="):
			session = strings.TrimPrefix(a, "--session=")
		case a == "--cwd":
			v, ok := value()
			if !ok {
				return writeExecUsageError(stderr, "--cwd requires a value")
			}
			cwd = v
		case strings.HasPrefix(a, "--cwd="):
			cwd = strings.TrimPrefix(a, "--cwd=")
		case a == "--prompt" || a == "-p":
			v, ok := value()
			if !ok {
				return writeExecUsageError(stderr, "--prompt requires a value")
			}
			prompt = v
		case strings.HasPrefix(a, "--prompt="):
			prompt = strings.TrimPrefix(a, "--prompt=")
		default:
			// Forwarded verbatim to the worker `zero exec` (reuses exec's own flag
			// parsing for per-session run options).
			forward = append(forward, a)
		}
	}
	if strings.TrimSpace(session) == "" {
		return writeExecUsageError(stderr, "daemon run requires --session <id>")
	}
	if prompt == "" && len(forward) == 0 {
		return writeExecUsageError(stderr, "daemon run requires --prompt <text> or exec args")
	}
	paths, err := daemon.DefaultPaths()
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	client, err := daemon.Dial(paths.Socket)
	if err != nil {
		return writeAppError(stderr, "zero daemon is not running (start it with `zero daemon start`)", exitCrash)
	}
	defer client.Close()
	if err := client.Run(session, cwd, prompt, forward, func(line string) { fmt.Fprintln(stdout, line) }); err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	return exitSuccess
}

func runDaemonAttach(args []string, stdout io.Writer, stderr io.Writer) int {
	session := ""
	extra := 0
	for _, a := range args {
		if a == "-h" || a == "--help" {
			return writeDaemonUsage(stdout, exitSuccess)
		}
		if session == "" {
			session = a
			continue
		}
		extra++
	}
	if strings.TrimSpace(session) == "" {
		return writeExecUsageError(stderr, "daemon attach requires a <session> id")
	}
	if extra > 0 {
		return writeExecUsageError(stderr, "daemon attach accepts exactly one <session> id")
	}
	paths, err := daemon.DefaultPaths()
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	client, err := daemon.Dial(paths.Socket)
	if err != nil {
		return writeAppError(stderr, "zero daemon is not running (start it with `zero daemon start`)", exitCrash)
	}
	defer client.Close()
	if err := client.Attach(session, func(line string) { fmt.Fprintln(stdout, line) }); err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	return exitSuccess
}

// daemonReachable reports whether a daemon is accepting connections (a successful
// handshake). Used for the single-instance check and start-up wait.
func daemonReachable(paths daemon.Paths) bool {
	client, err := daemon.Dial(paths.Socket)
	if err != nil {
		return false
	}
	_ = client.Close()
	return true
}

func helpRequested(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			return true
		}
	}
	return false
}
