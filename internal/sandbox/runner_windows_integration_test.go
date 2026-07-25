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

// TestWindowsUnelevatedRealSandboxSmoke exercises the unelevated tier end to
// end WITHOUT running the elevated setup helper: the command runner applies
// the workspace ACLs itself, a write inside the workspace succeeds under the
// restricted token, and a write outside every granted root is denied. Unlike
// the elevated smoke above it needs no Administrator terminal.
func TestWindowsUnelevatedRealSandboxSmoke(t *testing.T) {
	if os.Getenv("ZERO_SANDBOX_REAL_SMOKE") != "1" {
		t.Skip("set ZERO_SANDBOX_REAL_SMOKE=1 to run real Windows sandbox smoke tests")
	}
	runnerExe := realSmokeExecutable(t, "ZERO_WINDOWS_COMMAND_RUNNER_EXE", WindowsSandboxCommandRunnerName)

	root := t.TempDir()
	outside := t.TempDir()
	privateDir := filepath.Join(root, "private")
	if err := os.MkdirAll(privateDir, 0o700); err != nil {
		t.Fatalf("MkdirAll private: %v", err)
	}
	secretFile := filepath.Join(privateDir, "secret.txt")
	if err := os.WriteFile(secretFile, []byte("super-secret"), 0o600); err != nil {
		t.Fatalf("WriteFile secret: %v", err)
	}

	sandboxHome := filepath.Join(root, ".zero-sandbox")
	profile := PermissionProfile{
		FileSystem: FileSystemPolicy{
			Kind:                 FileSystemRestricted,
			ReadRoots:            []string{root},
			WriteRoots:           []WritableRoot{{Root: root, ProtectedMetadataNames: []string{".git", ".zero", ".agents"}}},
			DenyRead:             []string{privateDir},
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
		SandboxLevel:      WindowsSandboxLevelUnelevated,
	}

	// cmd.exe rather than powershell.exe: managed PowerShell cannot initialize
	// its crypto provider under a write-restricted token on some hosts
	// (0x8009001d, the same restricted-token limitation the runner documents for
	// Schannel), and the write-jail assertion only needs a native shell.
	insideMarker := filepath.Join(root, "unelevated-write-ok.txt")
	runWindowsRealSmokeCommand(t, runnerExe, config, []string{
		"cmd.exe", "/d", "/s", "/c", "echo ok>" + insideMarker,
	}, 0)
	if bytes, err := os.ReadFile(insideMarker); err != nil || strings.TrimSpace(string(bytes)) != "ok" {
		t.Fatalf("unelevated sandboxed write marker = %q, %v; want ok", bytes, err)
	}
	if _, err := os.Stat(WindowsUnelevatedSetupMarkerPath(sandboxHome)); err != nil {
		t.Fatalf("expected the unelevated setup marker to be recorded: %v", err)
	}

	// DenyRead check: reading from the privateDir must be blocked (exit code 1)
	runWindowsRealSmokeCommand(t, runnerExe, config, []string{
		"cmd.exe", "/d", "/s", "/c", "type " + secretFile,
	}, 1)

	outsideMarker := filepath.Join(outside, "unelevated-write-denied.txt")
	runWindowsRealSmokeCommand(t, runnerExe, config, []string{
		"cmd.exe", "/d", "/s", "/c", "echo leaked>" + outsideMarker,
	}, 1)
	if _, err := os.Stat(outsideMarker); err == nil {
		t.Fatalf("unelevated sandbox allowed a write outside every granted root")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat outside marker: %v", err)
	}
}

// TestWindowsRestrictedTokenNestedPipeCapture pins the fix in
// broadenWindowsRestrictedTokenDefaultDacl (windows_token_windows.go): a
// nested subprocess spawned FROM WITHIN the sandboxed process, with its
// output captured via a pipe, must succeed. Before the fix, the sandboxed
// process's own attempt to create a pipe for such a subprocess failed with
// ERROR_ACCESS_DENIED (Win32 error 5) — the WRITE_RESTRICTED token's extra
// access check has no restricted-SID match against a freshly created pipe's
// default security descriptor. This is exactly the pattern any tool that
// shells out internally and captures output hits (`gh` invoking `git`, for
// one; cmd.exe's own FOR /F does the identical CreatePipe+CreateProcess
// dance internally, so it reproduces the bug with no external dependency
// beyond cmd.exe itself).
func TestWindowsRestrictedTokenNestedPipeCapture(t *testing.T) {
	if os.Getenv("ZERO_SANDBOX_REAL_SMOKE") != "1" {
		t.Skip("set ZERO_SANDBOX_REAL_SMOKE=1 to run real Windows sandbox smoke tests")
	}
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
		SandboxLevel:      WindowsSandboxLevelUnelevated,
	}

	marker := filepath.Join(root, "nested-pipe-marker.txt")
	script := filepath.Join(root, "nested-pipe.cmd")
	// FOR /F drives cmd.exe's own internal CreatePipe+CreateProcess capture
	// of the quoted command's output — the exact mechanism this fix targets.
	// The full path to whoami.exe sidesteps PATH resolution inside the
	// sandboxed process's minimal environment.
	scriptBody := "for /F %%i in ('C:\\Windows\\System32\\whoami.exe') do echo %%i> " + marker + "\r\n"
	if err := os.WriteFile(script, []byte(scriptBody), 0o644); err != nil {
		t.Fatalf("write nested-pipe script: %v", err)
	}

	runWindowsRealSmokeCommand(t, runnerExe, config, []string{
		"cmd.exe", "/d", "/s", "/c", script,
	}, 0)
	captured, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read nested-pipe marker: %v", err)
	}
	if strings.TrimSpace(string(captured)) == "" {
		t.Fatalf("nested-pipe marker is empty; FOR /F failed to capture the subprocess's output")
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
