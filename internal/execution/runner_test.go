package execution

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

type capturedTestPreparer struct {
	request Request
}

func (preparer *capturedTestPreparer) PrepareExecution(_ context.Context, request Request) (PreparedCommand, error) {
	preparer.request = request
	name, args := testShellCommand("printf stdout; printf stderr >&2; exit 7")
	return PreparedCommand{
		Command: exec.Command(name, args...),
		Enforcement: Enforcement{
			Backend: "test-adapter",
			Level:   "native",
		},
	}, nil
}

func TestRunnerExecutesCapturedRequestThroughAdapter(t *testing.T) {
	preparer := &capturedTestPreparer{}
	runner := NewRunner(preparer)
	request := Request{
		Origin:           OriginHook,
		Mode:             ModeCaptured,
		Command:          Command{Name: "ignored-by-test-adapter"},
		WorkingDirectory: t.TempDir(),
		WorkspaceRoots:   []string{t.TempDir()},
		Approval:         ApprovalContext{PolicyVersion: PolicyVersion},
	}
	result := runner.ExecuteCaptured(context.Background(), CapturedRequest{Request: request})

	if preparer.request.Origin != OriginHook {
		t.Fatalf("adapter origin = %q, want hook", preparer.request.Origin)
	}
	if result.Stdout != "stdout" || result.Stderr != "stderr" {
		t.Fatalf("captured output = stdout %q stderr %q", result.Stdout, result.Stderr)
	}
	if result.Outcome.Kind != OutcomeApplicationFailure || result.Outcome.Exit == nil || result.Outcome.Exit.Code != 7 {
		t.Fatalf("outcome = %#v, want application failure exit 7", result.Outcome)
	}
	if result.Outcome.Enforcement.Backend != "test-adapter" {
		t.Fatalf("enforcement = %#v", result.Outcome.Enforcement)
	}
}

func TestRunnerWithoutAdapterFailsClosed(t *testing.T) {
	runner := NewRunner(nil)
	result := runner.ExecuteCaptured(context.Background(), CapturedRequest{Request: Request{
		Origin:           OriginPlugin,
		Mode:             ModeCaptured,
		Command:          Command{Name: "true"},
		WorkingDirectory: t.TempDir(),
		WorkspaceRoots:   []string{t.TempDir()},
	}})
	if result.Outcome.Kind != OutcomeSandboxSetupFailure || !strings.Contains(result.Stderr, "adapter") {
		t.Fatalf("result = %#v, want fail-closed missing-adapter result", result)
	}
}

func testShellCommand(script string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd.exe", []string{"/D", "/S", "/C", `<nul set /p "=stdout" & <nul set /p "=stderr" 1>&2 & exit /b 7`}
	}
	return "/bin/sh", []string{"-c", script}
}
