package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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
	default:
		fmt.Fprintln(os.Stderr, "unknown helper command")
		os.Exit(2)
	}
}

func TestCoreToolsExposeBashTool(t *testing.T) {
	toolset := CoreTools(t.TempDir())
	byName := make(map[string]Tool, len(toolset))
	for _, tool := range toolset {
		byName[tool.Name()] = tool
	}

	tool, ok := byName["bash"]
	if !ok {
		t.Fatalf("expected core tools to include bash")
	}
	if tool.Safety().SideEffect != SideEffectShell {
		t.Fatalf("bash side effect = %s, want shell", tool.Safety().SideEffect)
	}
	if tool.Safety().Permission != PermissionPrompt {
		t.Fatalf("bash permission = %s, want prompt", tool.Safety().Permission)
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
