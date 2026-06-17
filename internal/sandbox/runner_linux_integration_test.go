//go:build linux

package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLinuxHelperRealSandboxSmoke(t *testing.T) {
	if os.Getenv("ZERO_SANDBOX_REAL_SMOKE") != "1" {
		t.Skip("set ZERO_SANDBOX_REAL_SMOKE=1 to run real sandbox smoke tests")
	}
	backend := SelectBackend(BackendOptions{})
	if !backend.Available || backend.Name != BackendLinuxBwrap {
		t.Skipf("Linux sandbox backend unavailable: %s", backend.Message)
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("Mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "config"), []byte("[core]\n"), 0o644); err != nil {
		t.Fatalf("WriteFile .git/config: %v", err)
	}
	secretDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(secretDir, "secret.txt"), []byte("hidden\n"), 0o644); err != nil {
		t.Fatalf("WriteFile secret: %v", err)
	}
	blockedDir := filepath.Join(root, "blocked")
	if err := os.Mkdir(blockedDir, 0o755); err != nil {
		t.Fatalf("Mkdir blocked: %v", err)
	}

	policy := DefaultPolicy()
	policy.DenyRead = []string{secretDir}
	policy.DenyWrite = []string{blockedDir}
	engine := NewEngine(EngineOptions{WorkspaceRoot: root, Policy: policy, Backend: backend})
	output, runErr := runLinuxSandboxSmokeCommand(t, engine, CommandSpec{
		Name: "/bin/sh",
		Args: []string{"-c", strings.Join([]string{
			"set -eu",
			"echo ok > write-ok.txt",
			"test \"$(cat write-ok.txt)\" = ok",
			"echo tmp > /tmp/zero-sandbox-smoke",
			"test \"$(cat /tmp/zero-sandbox-smoke)\" = tmp",
			"cat .git/config >/dev/null",
		}, "\n")},
		Dir: root,
	})
	if runErr != nil {
		if linuxSandboxLaunchUnsupported(string(output)) {
			t.Skipf("Linux sandbox launch is unsupported in this environment: %v\n%s", runErr, output)
		}
		t.Fatalf("allowed smoke command failed: %v\n%s", runErr, output)
	}

	for _, tc := range []struct {
		name   string
		script string
		marker string
	}{
		{
			name:   "outside write",
			script: "if echo leak > /etc/zero_sandbox_smoke 2>/dev/null; then echo OUTSIDE_WRITE_SUCCEEDED; exit 42; fi",
			marker: "OUTSIDE_WRITE_SUCCEEDED",
		},
		{
			name:   "deny read",
			script: "if cat " + shellQuote(filepath.Join(secretDir, "secret.txt")) + " >/dev/null 2>&1; then echo DENY_READ_SUCCEEDED; exit 42; fi",
			marker: "DENY_READ_SUCCEEDED",
		},
		{
			name:   "deny write",
			script: "if echo leak > blocked/file 2>/dev/null; then echo DENY_WRITE_SUCCEEDED; exit 42; fi",
			marker: "DENY_WRITE_SUCCEEDED",
		},
		{
			name:   "metadata write",
			script: "if echo leak > .git/config 2>/dev/null; then echo METADATA_WRITE_SUCCEEDED; exit 42; fi",
			marker: "METADATA_WRITE_SUCCEEDED",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output, _ := runLinuxSandboxSmokeCommand(t, engine, CommandSpec{
				Name: "/bin/sh",
				Args: []string{"-c", tc.script},
				Dir:  root,
			})
			if strings.Contains(string(output), tc.marker) {
				t.Fatalf("sandbox allowed %s; output=%s", tc.name, output)
			}
		})
	}

	if python, err := exec.LookPath("python3"); err == nil && python != "" {
		t.Run("network deny", func(t *testing.T) {
			output, runErr := runLinuxSandboxSmokeCommand(t, engine, CommandSpec{
				Name: python,
				Args: []string{"-c", "import socket; socket.create_connection(('1.1.1.1', 80), 2).close()"},
				Dir:  root,
			})
			if runErr == nil {
				t.Fatalf("sandbox allowed outbound network; output=%s", output)
			}
		})
	} else {
		t.Log("python3 not found; skipping real network deny probe")
	}
}

func TestLinuxLandlockRealSandboxSmoke(t *testing.T) {
	if os.Getenv("ZERO_SANDBOX_REAL_SMOKE") != "1" {
		t.Skip("set ZERO_SANDBOX_REAL_SMOKE=1 to run real sandbox smoke tests")
	}
	helper, err := linuxSandboxHelperCommand()
	if err != nil {
		t.Skipf("Linux sandbox helper unavailable: %v", err)
	}
	root := t.TempDir()
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "blocked.txt")
	profile := PermissionProfile{
		FileSystem: FileSystemPolicy{
			Kind:                 FileSystemRestricted,
			ReadRoots:            []string{string(filepath.Separator)},
			WriteRoots:           []WritableRoot{{Root: root}},
			IncludePlatformRoots: true,
			AllowTemp:            true,
		},
		Network: NetworkPolicy{Mode: NetworkDeny},
	}
	output, runErr := runLinuxLandlockSmokeCommand(t, helper, profile, root, []string{"/bin/sh", "-c", strings.Join([]string{
		"set -eu",
		"echo ok > " + shellQuote(filepath.Join(root, "write-ok.txt")),
		"test \"$(cat " + shellQuote(filepath.Join(root, "write-ok.txt")) + ")\" = ok",
		"if echo leak > " + shellQuote(outsideFile) + " 2>/dev/null; then echo LANDLOCK_OUTSIDE_WRITE_SUCCEEDED; exit 42; fi",
	}, "\n")})
	if runErr != nil {
		if landlockLaunchUnsupported(string(output)) {
			t.Skipf("Landlock is unsupported in this environment: %v\n%s", runErr, output)
		}
		t.Fatalf("Landlock smoke command failed: %v\n%s", runErr, output)
	}
	if strings.Contains(string(output), "LANDLOCK_OUTSIDE_WRITE_SUCCEEDED") {
		t.Fatalf("Landlock allowed write outside approved roots: %s", output)
	}
	if _, err := os.Stat(outsideFile); err == nil {
		t.Fatalf("Landlock wrote host file outside approved roots: %s", outsideFile)
	}

	if python, err := exec.LookPath("python3"); err == nil && python != "" {
		output, runErr = runLinuxLandlockSmokeCommand(t, helper, profile, root, []string{
			python,
			"-c",
			"import socket; socket.create_connection(('1.1.1.1', 80), 2).close()",
		})
		if runErr == nil {
			t.Fatalf("Landlock mode allowed outbound network; output=%s", output)
		}
	} else {
		t.Log("python3 not found; skipping Landlock network deny probe")
	}
}

func runLinuxSandboxSmokeCommand(t *testing.T, engine *Engine, spec CommandSpec) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command, plan, err := engine.CommandContext(ctx, spec)
	if err != nil {
		t.Fatalf("CommandContext: %v", err)
	}
	output, runErr := command.CombinedOutput()
	if strings.Contains(string(output), "OUTSIDE_WRITE_SUCCEEDED") {
		t.Fatalf("sandbox allowed write outside workspace; plan=%#v output=%s", plan, output)
	}
	return output, runErr
}

func runLinuxLandlockSmokeCommand(t *testing.T, helper LinuxSandboxHelperCommand, profile PermissionProfile, root string, command []string) ([]byte, error) {
	t.Helper()
	args, err := BuildLinuxSandboxCommandArgs(LinuxSandboxCommandArgsOptions{
		SandboxPolicyCWD:  root,
		CommandCWD:        root,
		PermissionProfile: profile,
		UseLandlock:       true,
		Command:           command,
	})
	if err != nil {
		t.Fatalf("BuildLinuxSandboxCommandArgs: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, helper.Name, append(append([]string{}, helper.ArgsPrefix...), args...)...)
	if helper.Dir != "" {
		cmd.Dir = helper.Dir
	} else {
		cmd.Dir = root
	}
	cmd.Env = os.Environ()
	return cmd.CombinedOutput()
}

func linuxSandboxLaunchUnsupported(output string) bool {
	for _, marker := range []string{
		"Operation not permitted",
		"Permission denied",
		"Invalid argument",
		"No permissions to create new namespace",
		"creating new namespace failed",
		"bubblewrap is not available",
		"Can't mount proc on",
	} {
		if strings.Contains(output, marker) {
			return true
		}
	}
	return false
}

func landlockLaunchUnsupported(output string) bool {
	for _, marker := range []string{
		"apply Landlock: query ABI",
		"operation not supported",
		"Operation not supported",
		"invalid argument",
		"Invalid argument",
		"function not implemented",
		"Function not implemented",
	} {
		if strings.Contains(output, marker) {
			return true
		}
	}
	return false
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
