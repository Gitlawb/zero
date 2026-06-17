//go:build windows

package sandbox

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWindowsRestrictedTokenRealSandboxSmoke(t *testing.T) {
	if os.Getenv("ZERO_SANDBOX_REAL_SMOKE") != "1" {
		t.Skip("set ZERO_SANDBOX_REAL_SMOKE=1 to run real Windows sandbox smoke tests")
	}
	setupExe := realSmokeExecutable(t, "ZERO_WINDOWS_SANDBOX_SETUP_EXE", WindowsSandboxSetupName)
	runnerExe := realSmokeExecutable(t, "ZERO_WINDOWS_COMMAND_RUNNER_EXE", WindowsSandboxCommandRunnerName)

	root := t.TempDir()
	sandboxHome := filepath.Join(root, ".zero-sandbox")
	profile := PermissionProfile{
		FileSystem: FileSystemPolicy{
			Kind:                 FileSystemRestricted,
			ReadRoots:            []string{root},
			WriteRoots:           []WritableRoot{{Root: root, ProtectedMetadataNames: []string{".git", ".zero", ".agents"}}},
			IncludePlatformRoots: true,
			AllowTemp:            true,
		},
		Network: NetworkPolicy{Mode: NetworkDeny},
	}
	config := WindowsSandboxCommandArgsOptions{
		SandboxHome:       sandboxHome,
		CommandCWD:        root,
		WorkspaceRoots:    []string{root},
		PermissionProfile: profile,
		SandboxLevel:      WindowsSandboxLevelRestrictedToken,
	}
	runWindowsRealSmokeSetup(t, setupExe, WindowsSandboxSetupArgsOptions{
		SandboxHome:       sandboxHome,
		CommandCWD:        root,
		WorkspaceRoots:    []string{root},
		PermissionProfile: profile,
	})
	t.Cleanup(func() {
		cleanupProfile := profile
		cleanupProfile.Network = NetworkPolicy{Mode: NetworkAllow}
		runWindowsRealSmokeSetup(t, setupExe, WindowsSandboxSetupArgsOptions{
			SandboxHome:       sandboxHome,
			CommandCWD:        root,
			WorkspaceRoots:    []string{root},
			PermissionProfile: cleanupProfile,
		})
	})

	writeMarker := filepath.Join(root, "write-ok.txt")
	runWindowsRealSmokeCommand(t, runnerExe, config, []string{
		"powershell.exe",
		"-NoLogo",
		"-NoProfile",
		"-NonInteractive",
		"-ExecutionPolicy",
		"Bypass",
		"-Command",
		fmt.Sprintf("Set-Content -LiteralPath %s -Value ok", powershellSingleQuote(writeMarker)),
	}, 0)
	if bytes, err := os.ReadFile(writeMarker); err != nil || strings.TrimSpace(string(bytes)) != "ok" {
		t.Fatalf("sandboxed write marker = %q, %v; want ok", bytes, err)
	}

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen loopback for Windows network smoke: %v", err)
	}
	defer listener.Close()
	accepted := make(chan struct{}, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			_ = conn.Close()
			accepted <- struct{}{}
			return
		}
	}()

	networkAllowedMarker := filepath.Join(root, "network-allowed.txt")
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("parse listener address %q: %v", listener.Addr(), err)
	}
	script := fmt.Sprintf(`
$marker = %s
$client = [System.Net.Sockets.TcpClient]::new()
$connect = $client.BeginConnect('127.0.0.1', %s, $null, $null)
if ($connect.AsyncWaitHandle.WaitOne(1500, $false)) {
  try {
    $client.EndConnect($connect)
    Set-Content -LiteralPath $marker -Value allowed
    exit 42
  } catch {
    exit 0
  } finally {
    $client.Close()
  }
}
$client.Close()
exit 0
`, powershellSingleQuote(networkAllowedMarker), port)
	runWindowsRealSmokeCommand(t, runnerExe, config, []string{
		"powershell.exe",
		"-NoLogo",
		"-NoProfile",
		"-NonInteractive",
		"-ExecutionPolicy",
		"Bypass",
		"-Command",
		script,
	}, 0)
	if _, err := os.Stat(networkAllowedMarker); err == nil {
		t.Fatalf("Windows sandbox allowed loopback network connection under network deny")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat network marker: %v", err)
	}
	select {
	case <-accepted:
		t.Fatalf("Windows sandbox loopback listener accepted a denied connection")
	case <-time.After(200 * time.Millisecond):
	}
}

func realSmokeExecutable(t *testing.T, envKey string, fallbackName string) string {
	t.Helper()
	if path := os.Getenv(envKey); path != "" {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s=%q is not usable: %v", envKey, path, err)
		}
		return path
	}
	path, err := exec.LookPath(fallbackName)
	if err != nil {
		t.Skipf("%s is not available and %s is unset", fallbackName, envKey)
	}
	return path
}

func runWindowsRealSmokeSetup(t *testing.T, setupExe string, options WindowsSandboxSetupArgsOptions) {
	t.Helper()
	args, err := BuildWindowsSandboxSetupArgs(options)
	if err != nil {
		t.Fatalf("BuildWindowsSandboxSetupArgs: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, setupExe, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Windows sandbox setup failed: %v\n%s", err, output)
	}
}

func runWindowsRealSmokeCommand(t *testing.T, runnerExe string, base WindowsSandboxCommandArgsOptions, command []string, wantCode int) {
	t.Helper()
	base.Command = command
	args, err := BuildWindowsSandboxCommandArgs(base)
	if err != nil {
		t.Fatalf("BuildWindowsSandboxCommandArgs: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, runnerExe, args...)
	output, err := cmd.CombinedOutput()
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == wantCode {
		return
	}
	if err != nil {
		t.Fatalf("Windows sandbox command failed: %v\n%s", err, output)
	}
	if wantCode != 0 {
		t.Fatalf("Windows sandbox command exit code = 0, want %d\n%s", wantCode, output)
	}
}

func powershellSingleQuote(value string) string {
	out := "'"
	for _, r := range value {
		if r == '\'' {
			out += "''"
		} else {
			out += string(r)
		}
	}
	return out + "'"
}
