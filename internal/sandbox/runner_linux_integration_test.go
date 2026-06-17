//go:build linux

package sandbox

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestBubblewrapRealSandboxSmoke(t *testing.T) {
	if os.Getenv("ZERO_SANDBOX_REAL_SMOKE") != "1" {
		t.Skip("set ZERO_SANDBOX_REAL_SMOKE=1 to run real sandbox smoke tests")
	}
	backend := SelectBackend(BackendOptions{})
	if !backend.Available || backend.Name != BackendBubblewrap {
		t.Skipf("bubblewrap backend unavailable: %s", backend.Message)
	}
	root := t.TempDir()
	engine := NewEngine(EngineOptions{WorkspaceRoot: root, Policy: DefaultPolicy(), Backend: backend})
	command, plan, err := engine.CommandContext(context.Background(), CommandSpec{
		Name: "/bin/sh",
		Args: []string{"-c", strings.Join([]string{
			"set -eu",
			"echo ok > write-ok.txt",
			"test \"$(cat write-ok.txt)\" = ok",
			"echo tmp > /tmp/zero-sandbox-smoke",
			"if echo leak > /etc/zero_sandbox_smoke 2>/dev/null; then echo OUTSIDE_WRITE_SUCCEEDED; exit 42; fi",
		}, "\n")},
		Dir: root,
	})
	if err != nil {
		t.Fatalf("CommandContext: %v", err)
	}
	output, runErr := command.CombinedOutput()
	if strings.Contains(string(output), "OUTSIDE_WRITE_SUCCEEDED") {
		t.Fatalf("bubblewrap allowed write outside workspace; plan=%#v output=%s", plan, output)
	}
	if runErr != nil {
		t.Skipf("bubblewrap is present but real sandbox launch is unsupported in this environment: %v\n%s", runErr, output)
	}
}
