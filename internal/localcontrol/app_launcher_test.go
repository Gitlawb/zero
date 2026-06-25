package localcontrol

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

type fakeBrowserAppRunner struct {
	runPath   string
	runArgs   []string
	startPath string
	startArgs []string
	pid       int
	err       error
}

func (runner *fakeBrowserAppRunner) Run(_ context.Context, path string, args []string, _ []string, _ time.Duration) (CommandResult, error) {
	runner.runPath = path
	runner.runArgs = append([]string(nil), args...)
	return CommandResult{Path: path, Args: append([]string(nil), args...), ExitCode: 0}, nil
}

func (runner *fakeBrowserAppRunner) StartDetached(_ context.Context, path string, args []string, _ []string) (int, error) {
	runner.startPath = path
	runner.startArgs = append([]string(nil), args...)
	if runner.err != nil {
		return 0, runner.err
	}
	if runner.pid == 0 {
		runner.pid = 4321
	}
	return runner.pid, nil
}

func TestBrowserAppLauncherMapsDiscordToFlatpakDevToolsLaunch(t *testing.T) {
	requireLinuxFlatpakLaunch(t)
	runner := &fakeBrowserAppRunner{}
	launcher := NewBrowserAppLauncher(BrowserAppLaunchOptions{Runner: runner})

	result, err := launcher.LaunchBrowserApp(context.Background(), BrowserAppLaunchRequest{
		App:          "discord",
		DebugPort:    9222,
		StopExisting: true,
		Wait:         false,
	})
	if err != nil {
		t.Fatalf("LaunchBrowserApp returned error: %v", err)
	}
	if runner.runPath != "flatpak" {
		t.Fatalf("stop path = %q, want flatpak", runner.runPath)
	}
	if want := []string{"kill", "com.discordapp.Discord"}; !reflect.DeepEqual(runner.runArgs, want) {
		t.Fatalf("stop args = %#v, want %#v", runner.runArgs, want)
	}
	if runner.startPath != "flatpak" {
		t.Fatalf("start path = %q, want flatpak", runner.startPath)
	}
	wantStartArgs := []string{"run", "--command=com.discordapp.Discord", "com.discordapp.Discord", "--remote-debugging-port=9222"}
	if !reflect.DeepEqual(runner.startArgs, wantStartArgs) {
		t.Fatalf("start args = %#v, want %#v", runner.startArgs, wantStartArgs)
	}
	if result.PID != 4321 || result.DevToolsURL != "http://127.0.0.1:9222" {
		t.Fatalf("result = %#v, want pid/devtools URL", result)
	}
}

func TestBrowserAppLauncherDefaultsPortAndCanSkipStop(t *testing.T) {
	requireLinuxFlatpakLaunch(t)
	runner := &fakeBrowserAppRunner{}
	launcher := NewBrowserAppLauncher(BrowserAppLaunchOptions{Runner: runner})

	result, err := launcher.LaunchBrowserApp(context.Background(), BrowserAppLaunchRequest{
		App:  "discord",
		Wait: false,
	})
	if err != nil {
		t.Fatalf("LaunchBrowserApp returned error: %v", err)
	}
	if runner.runPath != "" {
		t.Fatalf("stop path = %q, want no stop", runner.runPath)
	}
	wantStartArgs := []string{"run", "--command=com.discordapp.Discord", "com.discordapp.Discord", "--remote-debugging-port=9222"}
	if !reflect.DeepEqual(runner.startArgs, wantStartArgs) {
		t.Fatalf("start args = %#v, want %#v", runner.startArgs, wantStartArgs)
	}
	if result.DebugPort != DefaultDevToolsPort {
		t.Fatalf("debug port = %d, want %d", result.DebugPort, DefaultDevToolsPort)
	}
}

func TestBrowserAppLauncherRejectsUnsupportedApp(t *testing.T) {
	launcher := NewBrowserAppLauncher(BrowserAppLaunchOptions{Runner: &fakeBrowserAppRunner{}})
	_, err := launcher.LaunchBrowserApp(context.Background(), BrowserAppLaunchRequest{App: "telegram"})
	if err == nil || !strings.Contains(err.Error(), "app must be one of") {
		t.Fatalf("error = %v, want unsupported app", err)
	}
}

func TestBrowserAppLauncherReportsStartFailure(t *testing.T) {
	requireLinuxFlatpakLaunch(t)
	launcher := NewBrowserAppLauncher(BrowserAppLaunchOptions{Runner: &fakeBrowserAppRunner{err: errors.New("boom")}})
	_, err := launcher.LaunchBrowserApp(context.Background(), BrowserAppLaunchRequest{App: "discord", Wait: false})
	if err == nil || !strings.Contains(err.Error(), "launch discord") {
		t.Fatalf("error = %v, want launch failure", err)
	}
}

func TestBrowserAppLauncherRejectsDiscordOnUnsupportedPlatform(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("discord launch is supported on Linux")
	}
	launcher := NewBrowserAppLauncher(BrowserAppLaunchOptions{Runner: &fakeBrowserAppRunner{}})
	_, err := launcher.LaunchBrowserApp(context.Background(), BrowserAppLaunchRequest{App: "discord", Wait: false})
	if err == nil || !strings.Contains(err.Error(), "only supported") {
		t.Fatalf("error = %v, want unsupported platform", err)
	}
}

func requireLinuxFlatpakLaunch(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("discord launch uses Linux flatpak")
	}
}
