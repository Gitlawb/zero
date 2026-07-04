package localcontrol

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

type fakeCommandRunner struct {
	path string
	args []string
	env  []string
}

func (runner *fakeCommandRunner) Run(_ context.Context, path string, args []string, env []string, _ time.Duration) (CommandResult, error) {
	runner.path = path
	runner.args = append([]string(nil), args...)
	runner.env = append([]string(nil), env...)
	return CommandResult{
		Path:     path,
		Args:     append([]string(nil), args...),
		Stdout:   "ok\n",
		ExitCode: 0,
	}, nil
}

func TestBrowserRunUsesConfiguredHelperPath(t *testing.T) {
	dir := t.TempDir()
	helper := filepath.Join(dir, "agent-browser")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write helper: %v", err)
	}
	runner := &fakeCommandRunner{}
	browser := NewBrowser(BrowserOptions{
		Enabled:    true,
		HelperPath: helper,
		Runner:     runner,
	})

	result, err := browser.Run(context.Background(), "open", "https://example.com")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.Output() != "ok" {
		t.Fatalf("Output = %q, want ok", result.Output())
	}
	if runner.path != helper {
		t.Fatalf("runner path = %q, want %q", runner.path, helper)
	}
	if want := []string{"open", "https://example.com"}; !reflect.DeepEqual(runner.args, want) {
		t.Fatalf("runner args = %#v, want %#v", runner.args, want)
	}
}

func TestBrowserRunDisabledFailsBeforeDiscovery(t *testing.T) {
	browser := NewBrowser(BrowserOptions{Enabled: false, HelperPath: "/does/not/exist"})
	_, err := browser.Run(context.Background(), "snapshot")
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("Run error = %v, want disabled", err)
	}
}

func TestBrowserRunMissingHelperPathIsActionable(t *testing.T) {
	browser := NewBrowser(BrowserOptions{Enabled: true, HelperPath: filepath.Join(t.TempDir(), "missing")})
	_, err := browser.Run(context.Background(), "snapshot")
	if err == nil || !strings.Contains(err.Error(), "helper not found") {
		t.Fatalf("Run error = %v, want missing helper", err)
	}
}

func TestBrowserRunUsesHelperManifest(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "node_modules", ".bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	helper := filepath.Join(binDir, "agent-browser")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write helper: %v", err)
	}
	t.Setenv(EnvHelperManifest, `{"version":1,"helpers":{"agent-browser":{"command":`+quoteJSON(helper)+`,"pathPrepend":[`+quoteJSON(binDir)+`],"env":{"ZERO_HELPER_TEST":"1"}}}}`)

	runner := &fakeCommandRunner{}
	browser := NewBrowser(BrowserOptions{
		Enabled: true,
		Runner:  runner,
	})

	if _, err := browser.Run(context.Background(), "snapshot"); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if runner.path != helper {
		t.Fatalf("runner path = %q, want manifest helper %q", runner.path, helper)
	}
	if !envContains(runner.env, "ZERO_HELPER_TEST=1") {
		t.Fatalf("env = %#v, want ZERO_HELPER_TEST", runner.env)
	}
	pathValue := envValue(runner.env, "PATH")
	if !strings.HasPrefix(pathValue, binDir+string(os.PathListSeparator)) && pathValue != binDir {
		t.Fatalf("PATH overlay = %q, want prefix %q", pathValue, binDir)
	}
}

func TestBrowserRunUsesManifestPrefixArgs(t *testing.T) {
	runner := &fakeCommandRunner{}
	t.Setenv(EnvHelperManifest, `{"version":1,"helpers":{"agent-browser":{"command":"cmd.exe","prefixArgs":["/d","/s","/c","C:\\zero\\agent-browser.cmd"]}}}`)
	browser := NewBrowser(BrowserOptions{
		Enabled: true,
		Runner:  runner,
	})

	if _, err := browser.Run(context.Background(), "open", "https://example.com"); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if runner.path != "cmd.exe" {
		t.Fatalf("runner path = %q, want cmd.exe", runner.path)
	}
	want := []string{"/d", "/s", "/c", `C:\zero\agent-browser.cmd`, "open", "https://example.com"}
	if !reflect.DeepEqual(runner.args, want) {
		t.Fatalf("runner args = %#v, want %#v", runner.args, want)
	}
}

func TestMergeEnvReplacesPathCaseInsensitivelyOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows env keys are case-insensitive")
	}
	env := mergeEnv([]string{`Path=C:\Windows`, "ZERO=1"}, []string{`PATH=C:\Zero`})
	pathCount := 0
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if !ok || !strings.EqualFold(key, "PATH") {
			continue
		}
		pathCount++
		if value != `C:\Zero` {
			t.Fatalf("PATH value = %q, want C:\\Zero", value)
		}
	}
	if pathCount != 1 {
		t.Fatalf("PATH entries = %d in %#v, want 1", pathCount, env)
	}
}

func TestBrowserRunUsesAdjacentPackagedHelper(t *testing.T) {
	root := t.TempDir()
	native := filepath.Join(root, "zero")
	if err := os.WriteFile(native, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write native: %v", err)
	}
	helpersDir := filepath.Join(root, "helpers")
	if err := os.MkdirAll(helpersDir, 0o755); err != nil {
		t.Fatalf("mkdir helpers: %v", err)
	}
	helper := filepath.Join(helpersDir, "agent-browser")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write helper: %v", err)
	}
	oldExecutablePath := executablePath
	executablePath = func() (string, error) { return native, nil }
	t.Cleanup(func() { executablePath = oldExecutablePath })

	runner := &fakeCommandRunner{}
	browser := NewBrowser(BrowserOptions{
		Enabled: true,
		Runner:  runner,
	})
	if _, err := browser.Run(context.Background(), "snapshot"); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if runner.path != helper {
		t.Fatalf("runner path = %q, want adjacent helper %q", runner.path, helper)
	}
}

func TestBrowserRunUsesAdjacentPackagedNodeBinHelper(t *testing.T) {
	root := t.TempDir()
	native := filepath.Join(root, "zero")
	if err := os.WriteFile(native, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write native: %v", err)
	}
	binDir := filepath.Join(root, "helpers", "node_modules", ".bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir helper bin: %v", err)
	}
	helper := filepath.Join(binDir, "agent-browser")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write helper: %v", err)
	}
	oldExecutablePath := executablePath
	executablePath = func() (string, error) { return native, nil }
	t.Cleanup(func() { executablePath = oldExecutablePath })

	runner := &fakeCommandRunner{}
	browser := NewBrowser(BrowserOptions{
		Enabled: true,
		Runner:  runner,
	})
	if _, err := browser.Run(context.Background(), "snapshot"); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if runner.path != helper {
		t.Fatalf("runner path = %q, want packaged node bin helper %q", runner.path, helper)
	}
}

func quoteJSON(value string) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func envContains(env []string, want string) bool {
	return slices.Contains(env, want)
}

func envValue(env []string, key string) string {
	for _, item := range env {
		if got, value, ok := strings.Cut(item, "="); ok && got == key {
			return value
		}
	}
	return ""
}
