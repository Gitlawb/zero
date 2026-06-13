package agenteval

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAgentCommandRunnerExpandsPlaceholdersAndUsesWorkspaceDir(t *testing.T) {
	workspace := t.TempDir()
	runner := CommandAgentRunner{Command: helperCommand(
		"record",
		filepath.Join(workspace, "args.txt"),
		"{task_id}",
		"{workspace}",
		"{prompt}",
		"{model}",
	)}

	result := runner.Run(context.Background(), AgentRunInput{
		TaskID:        "task-a",
		WorkspacePath: workspace,
		Prompt:        "fix bug",
		Model:         "gpt-5",
	})

	if result.ExitCode != 0 || result.Error != "" {
		t.Fatalf("Run = %#v", result)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "args.txt"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	want := []string{"cwd=" + workspace, "task-a", workspace, "fix bug", "gpt-5"}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("recorded args = %#v, want %#v", lines, want)
	}
}

func TestCommandAgentRunnerCapturesStdoutStderrAndExitCode(t *testing.T) {
	workspace := t.TempDir()
	runner := CommandAgentRunner{Command: helperCommand("exit", "7")}

	result := runner.Run(context.Background(), AgentRunInput{WorkspacePath: workspace})

	if result.ExitCode != 7 {
		t.Fatalf("ExitCode = %d, want 7; result=%#v", result.ExitCode, result)
	}
	if result.Error != "" {
		t.Fatalf("Error = %q, want empty for non-zero exit", result.Error)
	}
	if result.Stdout != "agent stdout\n" {
		t.Fatalf("Stdout = %q", result.Stdout)
	}
	if result.Stderr != "agent stderr\n" {
		t.Fatalf("Stderr = %q", result.Stderr)
	}
}

func TestCommandAgentRunnerTruncatesOversizedOutput(t *testing.T) {
	runner := CommandAgentRunner{
		Command:     helperCommand("spew", "5000"),
		OutputLimit: 1000,
	}

	result := runner.Run(context.Background(), AgentRunInput{WorkspacePath: t.TempDir()})

	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; result=%#v", result.ExitCode, result)
	}
	if !result.Truncated {
		t.Fatalf("expected Truncated=true; stdout len=%d", len(result.Stdout))
	}
	if len(result.Stdout) != 1000 {
		t.Fatalf("captured stdout len = %d, want 1000", len(result.Stdout))
	}
}

func TestCommandAgentRunnerTimesOutHangingAgent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	runner := CommandAgentRunner{Command: helperCommand("sleep")}

	start := time.Now()
	result := runner.Run(ctx, AgentRunInput{WorkspacePath: t.TempDir()})

	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("runner did not honor timeout; elapsed=%s", elapsed)
	}
	if result.ExitCode != -1 {
		t.Fatalf("ExitCode = %d, want -1; result=%#v", result.ExitCode, result)
	}
	if result.Error == "" {
		t.Fatalf("expected a timeout error; result=%#v", result)
	}
}

func TestExpandAgentCommandDoesNotReexpandInjectedPlaceholders(t *testing.T) {
	got := expandAgentCommand(
		[]string{"agent", "{prompt}", "{task_id}"},
		AgentRunInput{Prompt: "edit {workspace} now", WorkspacePath: "/ws", TaskID: "t1"},
	)

	want := []string{"agent", "edit {workspace} now", "t1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expandAgentCommand = %#v, want %#v", got, want)
	}
}

func TestCommandAgentRunnerEmptyCommandReturnsError(t *testing.T) {
	tests := []struct {
		name    string
		command []string
	}{
		{name: "nil", command: nil},
		{name: "empty executable", command: []string{""}},
		{name: "blank executable", command: []string{"  "}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := (CommandAgentRunner{Command: tt.command}).Run(context.Background(), AgentRunInput{})

			if result.ExitCode != -1 {
				t.Fatalf("ExitCode = %d, want -1", result.ExitCode)
			}
			if result.Error != "agent command is required" {
				t.Fatalf("Error = %q", result.Error)
			}
		})
	}
}

func TestCommandAgentRunnerRequiresWorkspacePath(t *testing.T) {
	result := (CommandAgentRunner{Command: helperCommand("record", filepath.Join(t.TempDir(), "out.txt"))}).
		Run(context.Background(), AgentRunInput{})

	if result.ExitCode != -1 {
		t.Fatalf("ExitCode = %d, want -1", result.ExitCode)
	}
	if result.Error != "workspace path is required" {
		t.Fatalf("Error = %q", result.Error)
	}
}

func TestCommandAgentRunnerReportsContextErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := (CommandAgentRunner{Command: helperCommand("record", filepath.Join(t.TempDir(), "out.txt"))}).
		Run(ctx, AgentRunInput{WorkspacePath: t.TempDir()})

	if result.ExitCode != -1 {
		t.Fatalf("ExitCode = %d, want -1", result.ExitCode)
	}
	if result.Error == "" {
		t.Fatalf("Error is empty; result=%#v", result)
	}
}

func TestCommandAgentRunnerReportsSpawnErrors(t *testing.T) {
	result := (CommandAgentRunner{Command: []string{filepath.Join(t.TempDir(), "missing-agent")}}).
		Run(context.Background(), AgentRunInput{WorkspacePath: t.TempDir()})

	if result.ExitCode != -1 {
		t.Fatalf("ExitCode = %d, want -1", result.ExitCode)
	}
	if result.Error == "" {
		t.Fatalf("Error is empty; result=%#v", result)
	}
}

func TestAgentRunnerFuncRunCallsFunction(t *testing.T) {
	called := false
	runner := AgentRunnerFunc(func(_ context.Context, input AgentRunInput) AgentRunResult {
		called = true
		if input.TaskID != "task-a" {
			t.Fatalf("TaskID = %q", input.TaskID)
		}
		return AgentRunResult{ExitCode: 3, Stdout: "ok"}
	})

	result := runner.Run(context.Background(), AgentRunInput{TaskID: "task-a"})

	if !called {
		t.Fatal("function was not called")
	}
	if result.ExitCode != 3 || result.Stdout != "ok" {
		t.Fatalf("Run = %#v", result)
	}
}

func TestCommandAgentRunnerHelperProcess(t *testing.T) {
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		return
	}
	if len(args) < 2 {
		os.Exit(2)
	}
	switch args[1] {
	case "record":
		if len(args) < 3 {
			os.Exit(2)
		}
		cwd, err := os.Getwd()
		if err != nil {
			os.Exit(2)
		}
		lines := append([]string{"cwd=" + cwd}, args[3:]...)
		if err := os.WriteFile(args[2], []byte(strings.Join(lines, "\n")), 0o600); err != nil {
			os.Exit(2)
		}
	case "exit":
		if _, err := os.Stdout.WriteString("agent stdout\n"); err != nil {
			os.Exit(2)
		}
		if _, err := os.Stderr.WriteString("agent stderr\n"); err != nil {
			os.Exit(2)
		}
		os.Exit(7)
	case "spew":
		if len(args) < 3 {
			os.Exit(2)
		}
		count, err := strconv.Atoi(args[2])
		if err != nil {
			os.Exit(2)
		}
		if _, err := os.Stdout.Write([]byte(strings.Repeat("a", count))); err != nil {
			os.Exit(2)
		}
	case "sleep":
		time.Sleep(30 * time.Second)
	default:
		os.Exit(2)
	}
	os.Exit(0)
}

func helperCommand(args ...string) []string {
	command := []string{
		os.Args[0],
		"-test.run=TestCommandAgentRunnerHelperProcess",
		"--",
	}
	command = append(command, args...)
	return command
}
