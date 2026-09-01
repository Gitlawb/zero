package execution

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

type capturedTestPreparer struct {
	request Request
}

func (preparer *capturedTestPreparer) PrepareExecution(_ context.Context, request Request) (PreparedCommand, error) {
	preparer.request = request
	command := exec.Command(os.Args[0], "-test.run=^TestCapturedRunnerHelperProcess$")
	command.Env = append(os.Environ(), "ZERO_CAPTURED_RUNNER_HELPER=1")
	return PreparedCommand{
		Command: command,
		Enforcement: Enforcement{
			Backend: "test-adapter",
			Level:   "native",
		},
	}, nil
}

func TestCapturedRunnerHelperProcess(t *testing.T) {
	if os.Getenv("ZERO_CAPTURED_RUNNER_HELPER") != "1" {
		return
	}
	fmt.Fprint(os.Stdout, "stdout")
	fmt.Fprint(os.Stderr, "stderr")
	os.Exit(7)
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
