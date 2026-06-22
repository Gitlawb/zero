package tools

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/sandbox"
)

func TestMain(m *testing.M) {
	if len(os.Args) >= 3 && os.Args[1] == "--zero-bash-helper" {
		runBashToolHelper(os.Args[2])
		return
	}

	os.Exit(m.Run())
}

func runBashToolHelper(command string) {
	switch command {
	case "success":
		fmt.Println("hello from bash")
	case "stderr":
		fmt.Fprintln(os.Stderr, "warning from bash")
	case "fail":
		fmt.Println("before failure")
		fmt.Fprintln(os.Stderr, "failure details")
		os.Exit(7)
	case "pwd":
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(cwd)
	case "sleep":
		time.Sleep(250 * time.Millisecond)
		fmt.Println("woke up")
	case "long-sleep":
		time.Sleep(5 * time.Second)
		fmt.Println("long sleep finished")
	case "http-server":
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("listening", listener.Addr().String())
		server := &http.Server{
			Handler: http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				_, _ = response.Write([]byte("zero-server-ok"))
			}),
		}
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "unknown helper command")
		os.Exit(2)
	}
}

func TestCoreToolsExposeShellTools(t *testing.T) {
	toolset := CoreTools(t.TempDir())
	byName := make(map[string]Tool, len(toolset))
	for _, tool := range toolset {
		byName[tool.Name()] = tool
	}

	for _, name := range []string{"exec_command", "write_stdin", "bash"} {
		tool, ok := byName[name]
		if !ok {
			t.Fatalf("expected core tools to include %s", name)
		}
		if tool.Safety().SideEffect != SideEffectShell {
			t.Fatalf("%s side effect = %s, want shell", name, tool.Safety().SideEffect)
		}
		wantPermission := PermissionPrompt
		if tool.Safety().Permission != wantPermission {
			t.Fatalf("%s permission = %s, want %s", name, tool.Safety().Permission, wantPermission)
		}
		if name == "write_stdin" && !tool.Safety().AdvertiseInAuto {
			t.Fatalf("write_stdin should stay visible in auto mode for polling and interrupts")
		}
	}
}

func TestBashToolDescribesHostShellSyntax(t *testing.T) {
	tool := NewBashTool(t.TempDir())
	schema := tool.Parameters()
	descriptionParts := []string{tool.Description()}
	for _, property := range schema.Properties {
		descriptionParts = append(descriptionParts, property.Description)
	}
	description := strings.ToLower(strings.Join(descriptionParts, " "))
	if !strings.Contains(description, "sandbox_permissions") || !strings.Contains(description, "require_escalated") {
		t.Fatalf("expected sandbox escalation guidance in bash description, got %q", description)
	}

	if runtime.GOOS == "windows" {
		if !strings.Contains(description, "cmd.exe") || !strings.Contains(description, "cwd") {
			t.Fatalf("expected Windows cmd.exe and cwd guidance in bash description, got %q", description)
		}
		return
	}
	if !strings.Contains(description, "/bin/sh") {
		t.Fatalf("expected /bin/sh guidance in bash description, got %q", description)
	}
}

func TestDetectShellCommandIssueFlagsWindowsBashisms(t *testing.T) {
	issue := detectShellCommandIssue(`cd /d/tmp/zero-pr-158 && ls -la`, "windows")
	if issue == nil {
		t.Fatal("expected Windows bash-style cd command to be flagged")
	}
	for _, want := range []string{"Windows cmd.exe", "cwd", "list_directory"} {
		if !strings.Contains(issue.Message+" "+issue.Suggestion, want) {
			t.Fatalf("expected issue to mention %q, got %#v", want, issue)
		}
	}
}

func TestDetectShellCommandIssueAllowsWindowsCDSwitch(t *testing.T) {
	issue := detectShellCommandIssue(`cd /d D:\tmp\zero-pr-158 && dir`, "windows")
	if issue != nil {
		t.Fatalf("expected valid Windows cd /d switch to pass, got %#v", issue)
	}
}

func TestDetectShellCommandIssueRequiresActualLSCommand(t *testing.T) {
	for _, command := range []string{
		`echo false ls -la`,
		`echo list -items`,
		`powershell -NoProfile -Command "Write-Output ls -la"`,
	} {
		if issue := detectShellCommandIssue(command, "windows"); issue != nil {
			t.Fatalf("expected incidental ls text to pass for %q, got %#v", command, issue)
		}
	}

	for _, command := range []string{
		`ls -la`,
		`cd C:\tmp && ls -la`,
		`cd C:\tmp && ls`,
	} {
		if issue := detectShellCommandIssue(command, "windows"); issue == nil {
			t.Fatalf("expected actual ls command to be flagged for %q", command)
		}
	}
}

func TestDetectShellOutputIssueAddsWindowsSyntaxHint(t *testing.T) {
	issue := detectShellOutputIssue(`cd /d/tmp/zero-pr-158 && ls -la`, "The syntax of the command is incorrect.", "windows")
	if issue == nil {
		t.Fatal("expected Windows syntax error to get shell guidance")
	}
	rendered := appendShellIssueHint("stderr:\nThe syntax of the command is incorrect.\nexit_code: 1", *issue)
	for _, want := range []string{"[zero] shell issue:", "Windows cmd.exe", "Suggestion:"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected rendered hint to contain %q, got %q", want, rendered)
		}
	}
}

func TestRegistryBlocksBashWithoutGrant(t *testing.T) {
	registry := NewRegistry()
	registry.Register(NewBashTool(t.TempDir()))

	result := registry.Run(context.Background(), "bash", map[string]any{
		"command": helperCommand("success"),
	})

	if result.Status != StatusError {
		t.Fatalf("expected error status, got %s", result.Status)
	}
	if !strings.Contains(result.Output, "Permission required for bash") {
		t.Fatalf("expected permission error, got %q", result.Output)
	}
}

func TestBashToolRunsCommandInWorkspace(t *testing.T) {
	root := t.TempDir()

	result := NewBashTool(root).Run(context.Background(), map[string]any{
		"command": helperCommand("success"),
	})

	if result.Status != StatusOK {
		t.Fatalf("expected ok status, got %s: %s", result.Status, result.Output)
	}
	if !strings.Contains(result.Output, "stdout:\nhello from bash") {
		t.Fatalf("expected stdout in output, got %q", result.Output)
	}
	if result.Meta["exit_code"] != "0" {
		t.Fatalf("expected exit_code metadata 0, got %q", result.Meta["exit_code"])
	}
	if result.Meta["cwd"] != "." {
		t.Fatalf("expected cwd metadata ., got %q", result.Meta["cwd"])
	}
}

func TestBashToolUsesRequestedCwd(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	result := NewBashTool(root).Run(context.Background(), map[string]any{
		"command": helperCommand("pwd"),
		"cwd":     "nested",
	})

	if result.Status != StatusOK {
		t.Fatalf("expected ok status, got %s: %s", result.Status, result.Output)
	}
	normalizedOutput := filepath.ToSlash(strings.TrimSpace(result.Output))
	if !strings.Contains(normalizedOutput, "stdout:\n") || !strings.HasSuffix(normalizedOutput, "/nested") {
		t.Fatalf("expected command to run in nested cwd, got %q", result.Output)
	}
	if result.Meta["cwd"] != "nested" {
		t.Fatalf("expected cwd metadata nested, got %q", result.Meta["cwd"])
	}
}

func TestBashToolRejectsCwdOutsideWorkspace(t *testing.T) {
	outside := t.TempDir()

	result := NewBashTool(t.TempDir()).Run(context.Background(), map[string]any{
		"command": helperCommand("success"),
		"cwd":     outside,
	})

	if result.Status != StatusError {
		t.Fatalf("expected error status, got %s", result.Status)
	}
	if !strings.Contains(result.Output, "must stay inside the workspace") {
		t.Fatalf("expected workspace error, got %q", result.Output)
	}
}

func TestBashToolReturnsNonzeroExitAsError(t *testing.T) {
	result := NewBashTool(t.TempDir()).Run(context.Background(), map[string]any{
		"command": helperCommand("fail"),
	})

	if result.Status != StatusError {
		t.Fatalf("expected error status, got %s", result.Status)
	}
	for _, want := range []string{"stdout:\nbefore failure", "stderr:\nfailure details", "exit_code: 7"} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("expected output to contain %q, got %q", want, result.Output)
		}
	}
	if result.Meta["exit_code"] != "7" {
		t.Fatalf("expected exit_code metadata 7, got %q", result.Meta["exit_code"])
	}
}

func TestBashToolTimesOut(t *testing.T) {
	result := NewBashTool(t.TempDir()).Run(context.Background(), map[string]any{
		"command":    helperCommand("sleep"),
		"timeout_ms": 20,
	})

	if result.Status != StatusError {
		t.Fatalf("expected error status, got %s", result.Status)
	}
	if !strings.Contains(result.Output, "timed out after 20ms") {
		t.Fatalf("expected timeout error, got %q", result.Output)
	}
	if result.Meta["timeout_ms"] != "20" {
		t.Fatalf("expected timeout_ms metadata 20, got %q", result.Meta["timeout_ms"])
	}
}

func TestBashToolTimeoutKillsBackgroundChildren(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group kill is POSIX-only")
	}
	// Shorten the post-kill I/O drain so this regression doesn't bake several
	// seconds into every run; the child sleep below is correspondingly short.
	defer func(prev time.Duration) { bashWaitDelay = prev }(bashWaitDelay)
	bashWaitDelay = 200 * time.Millisecond

	root := t.TempDir()
	sentinel := filepath.Join(root, "leaked")
	// A backgrounded child sleeps past the timeout, then drops a sentinel. `wait`
	// keeps the foreground shell alive so the deadline fires while the child is
	// still running. Without process-group kill the child is orphaned: it
	// survives, eventually writes the sentinel, and (because it inherited the
	// stdout pipe) blocks Run() until it exits.
	const childSleep = time.Second
	command := fmt.Sprintf("(sleep 1; touch %s) & wait", shellQuote(sentinel))

	start := time.Now()
	result := NewBashTool(root).Run(context.Background(), map[string]any{
		"command":    command,
		"timeout_ms": 300,
	})
	elapsed := time.Since(start)

	if result.Status != StatusError {
		t.Fatalf("expected timeout error status, got %s: %q", result.Status, result.Output)
	}
	if elapsed > time.Second {
		t.Fatalf("Run blocked %s past the 300ms timeout; background child held the pipes", elapsed)
	}

	// Give the child more than its sleep to fire if it survived the timeout.
	time.Sleep(childSleep + 500*time.Millisecond)
	if _, err := os.Stat(sentinel); err == nil {
		t.Fatalf("background child survived the timeout and wrote %s", sentinel)
	} else if !os.IsNotExist(err) {
		t.Fatalf("unexpected error stat-ing sentinel: %v", err)
	}
}

func TestRegistryReportsUnavailableNativeSandbox(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	registry.Register(NewBashTool(root))
	engine := sandbox.NewEngine(sandbox.EngineOptions{
		WorkspaceRoot: root,
		Policy:        sandbox.DefaultPolicy(),
		Backend:       sandbox.Backend{Name: sandbox.BackendUnavailable, Message: "native sandbox unavailable"},
	})

	result := registry.RunWithOptions(context.Background(), "bash", map[string]any{
		"command": helperCommand("success"),
	}, RunOptions{
		PermissionGranted: true,
		Sandbox:           engine,
		PermissionMode:    string(sandbox.PermissionUnsafe),
		Autonomy:          "medium",
	})

	if result.Status != StatusError {
		t.Fatalf("expected error status, got %s: %s", result.Status, result.Output)
	}
	if !strings.Contains(result.Output, "native sandbox backend is unavailable") {
		t.Fatalf("expected unavailable native sandbox error, got %q", result.Output)
	}
}

func TestBashToolReportsUnavailableNativeSandbox(t *testing.T) {
	root := t.TempDir()
	policy := sandbox.DefaultPolicy()
	engine := sandbox.NewEngine(sandbox.EngineOptions{
		WorkspaceRoot: root,
		Policy:        policy,
		Backend:       sandbox.Backend{Name: sandbox.BackendUnavailable, Message: "native sandbox unavailable"},
	})

	result := NewBashTool(root).(interface {
		RunWithSandbox(context.Context, map[string]any, *sandbox.Engine) Result
	}).RunWithSandbox(context.Background(), map[string]any{
		"command": helperCommand("success"),
	}, engine)

	if result.Status != StatusError {
		t.Fatalf("expected error status, got %s", result.Status)
	}
	if !strings.Contains(result.Output, "native sandbox backend is unavailable") {
		t.Fatalf("expected unavailable native sandbox error, got %q", result.Output)
	}
	if result.Meta["exit_code"] != "-1" {
		t.Fatalf("exit_code metadata = %q, want -1", result.Meta["exit_code"])
	}
}

func TestBashToolBuildsWrappedSandboxExecCommand(t *testing.T) {
	root := t.TempDir()
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", root, err)
	}
	engine := sandbox.NewEngine(sandbox.EngineOptions{
		WorkspaceRoot: root,
		Policy:        sandbox.DefaultPolicy(),
		Backend: sandbox.Backend{
			Name:       sandbox.BackendMacOSSeatbelt,
			Available:  true,
			Executable: "/usr/bin/sandbox-exec",
		},
	})

	command, plan, err := buildBashCommand(context.Background(), "pwd", root, engine)
	if err != nil {
		t.Fatalf("buildBashCommand: %v", err)
	}
	if command.Path != "/usr/bin/sandbox-exec" || !plan.Wrapped {
		t.Fatalf("command path = %q plan = %#v, want wrapped sandbox-exec", command.Path, plan)
	}
	if len(command.Args) < 5 || command.Args[1] != "-p" || !strings.Contains(command.Args[2], "(deny network*)") {
		t.Fatalf("sandbox-exec args = %#v, want inline profile", command.Args)
	}
	if command.Dir != resolvedRoot || plan.SandboxDir != resolvedRoot {
		t.Fatalf("dirs = command %q plan %q, want root", command.Dir, plan.SandboxDir)
	}
}

func TestBashToolRunsWithHostSandboxBackendWhenAvailable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows sandbox adapter is owned by the Windows integration slice")
	}
	backend := sandbox.SelectBackend(sandbox.BackendOptions{})
	if !backend.Available || backend.Name == sandbox.BackendUnavailable {
		t.Skipf("host sandbox backend unavailable: %s", backend.Message)
	}
	root := t.TempDir()
	engine := sandbox.NewEngine(sandbox.EngineOptions{
		WorkspaceRoot: root,
		Policy:        sandbox.DefaultPolicy(),
		Backend:       backend,
	})

	result := NewBashTool(root).(interface {
		RunWithSandbox(context.Context, map[string]any, *sandbox.Engine) Result
	}).RunWithSandbox(context.Background(), map[string]any{
		"command": "printf sandbox-ok",
	}, engine)

	if result.Status != StatusOK {
		t.Fatalf("expected host sandbox command to run, got %s: %s; meta=%#v", result.Status, result.Output, result.Meta)
	}
	if !strings.Contains(result.Output, "sandbox-ok") {
		t.Fatalf("expected sandbox command output, got %q", result.Output)
	}
	if result.Meta["sandbox_backend"] != string(backend.Name) || result.Meta["sandbox_wrapped"] != "true" {
		t.Fatalf("sandbox metadata = %#v, want wrapped host backend %s", result.Meta, backend.Name)
	}
}

func TestBashToolBlocksInteractiveCommandBeforeExecution(t *testing.T) {
	root := t.TempDir()

	result := NewBashTool(root).Run(context.Background(), map[string]any{
		"command": "vim main.go",
	})

	if result.Status != StatusError {
		t.Fatalf("expected error status for interactive command, got %s: %s", result.Status, result.Output)
	}
	if !strings.Contains(result.Output, "interactive") {
		t.Fatalf("expected interactive guard message, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "edit_file") && !strings.Contains(result.Output, "sed") {
		t.Fatalf("expected actionable suggestion, got %q", result.Output)
	}
	// The command must NOT have run: exit_code metadata should mark a pre-exec block.
	if result.Meta["exit_code"] != "-1" {
		t.Fatalf("expected exit_code -1 (not executed), got %q", result.Meta["exit_code"])
	}
	if result.Meta["safety_block"] != "interactive_command" {
		t.Fatalf("expected safety_block metadata, got %#v", result.Meta)
	}
	if result.Display.Kind != "shell" || !strings.Contains(result.Display.Summary, "Blocked") {
		t.Fatalf("expected blocked display annotation, got %#v", result.Display)
	}
}

func TestBashToolBlocksInteractiveCommandThroughSandbox(t *testing.T) {
	root := t.TempDir()
	engine := sandbox.NewEngine(sandbox.EngineOptions{
		WorkspaceRoot: root,
		Policy:        sandbox.DefaultPolicy(),
		Backend:       sandbox.Backend{Name: sandbox.BackendUnavailable, Message: "native sandbox unavailable"},
	})

	result := NewBashTool(root).(interface {
		RunWithSandbox(context.Context, map[string]any, *sandbox.Engine) Result
	}).RunWithSandbox(context.Background(), map[string]any{
		"command": "less /etc/hosts",
	}, engine)

	if result.Status != StatusError {
		t.Fatalf("expected error status, got %s: %s", result.Status, result.Output)
	}
	if !strings.Contains(result.Output, "interactive") || !strings.Contains(result.Output, "cat") {
		t.Fatalf("expected pager guard message with cat suggestion, got %q", result.Output)
	}
}

func TestBashToolAllowsNonInteractiveCommand(t *testing.T) {
	root := t.TempDir()

	result := NewBashTool(root).Run(context.Background(), map[string]any{
		"command": helperCommand("success"),
	})

	if result.Status != StatusOK {
		t.Fatalf("expected ok status for non-interactive command, got %s: %s", result.Status, result.Output)
	}
	if result.Meta["safety_block"] != "" {
		t.Fatalf("did not expect a safety block, got %#v", result.Meta)
	}
}

func helperCommand(name string) string {
	executable := shellQuote(os.Args[0])
	return executable + " --zero-bash-helper " + name
}

func shellQuote(value string) string {
	if runtime.GOOS == "windows" {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func TestAppendSandboxBlocks(t *testing.T) {
	if got := appendSandboxBlocks("err output", nil); got != "err output" {
		t.Fatalf("no blocks must leave stderr unchanged, got %q", got)
	}
	got := appendSandboxBlocks("err output", []string{"deny file-write-create /etc/x"})
	for _, want := range []string{"err output", "<sandbox_blocks>", "deny file-write-create /etc/x", "</sandbox_blocks>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("annotated stderr missing %q:\n%s", want, got)
		}
	}
	if block := appendSandboxBlocks("", []string{"deny x"}); strings.HasPrefix(block, "\n") {
		t.Fatalf("empty stderr must not yield a leading newline: %q", block)
	}
}
