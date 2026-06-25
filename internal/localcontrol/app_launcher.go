package localcontrol

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultDevToolsPort        = 9222
	defaultDevToolsWaitTimeout = 20 * time.Second
)

type BrowserAppLaunchOptions struct {
	Runner      BrowserAppRunner
	WaitTimeout time.Duration
}

type BrowserAppLaunchRequest struct {
	App          string
	DebugPort    int
	StopExisting bool
	Wait         bool
}

type BrowserAppLaunchResult struct {
	App         string
	Command     string
	Args        []string
	PID         int
	DebugPort   int
	DevToolsURL string
}

type BrowserAppRunner interface {
	Run(ctx context.Context, path string, args []string, env []string, timeout time.Duration) (CommandResult, error)
	StartDetached(ctx context.Context, path string, args []string, env []string) (int, error)
}

type BrowserAppLauncher struct {
	runner      BrowserAppRunner
	waitTimeout time.Duration
}

type browserAppSpec struct {
	name     string
	command  string
	args     []string
	env      []string
	stopPath string
	stopArgs []string
}

func NewBrowserAppLauncher(options BrowserAppLaunchOptions) BrowserAppLauncher {
	if options.Runner == nil {
		options.Runner = ExecBrowserAppRunner{}
	}
	if options.WaitTimeout <= 0 {
		options.WaitTimeout = defaultDevToolsWaitTimeout
	}
	return BrowserAppLauncher{
		runner:      options.Runner,
		waitTimeout: options.WaitTimeout,
	}
}

func (launcher BrowserAppLauncher) LaunchBrowserApp(ctx context.Context, request BrowserAppLaunchRequest) (BrowserAppLaunchResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	port := request.DebugPort
	if port == 0 {
		port = DefaultDevToolsPort
	}
	if port < 1024 || port > 65535 {
		return BrowserAppLaunchResult{}, fmt.Errorf("debug_port must be between 1024 and 65535")
	}
	spec, err := browserAppLaunchSpec(request.App, port)
	if err != nil {
		return BrowserAppLaunchResult{}, err
	}

	if request.StopExisting && spec.stopPath != "" {
		_, _ = launcher.runner.Run(ctx, spec.stopPath, spec.stopArgs, nil, 5*time.Second)
	}

	pid, err := launcher.runner.StartDetached(ctx, spec.command, spec.args, spec.env)
	if err != nil {
		return BrowserAppLaunchResult{}, fmt.Errorf("launch %s: %w", spec.name, err)
	}

	devToolsURL := "http://127.0.0.1:" + strconv.Itoa(port)
	if request.Wait {
		if err := waitForDevTools(ctx, devToolsURL, launcher.waitTimeout); err != nil {
			return BrowserAppLaunchResult{}, err
		}
	}

	return BrowserAppLaunchResult{
		App:         spec.name,
		Command:     spec.command,
		Args:        append([]string(nil), spec.args...),
		PID:         pid,
		DebugPort:   port,
		DevToolsURL: devToolsURL,
	}, nil
}

func browserAppLaunchSpec(app string, port int) (browserAppSpec, error) {
	switch strings.ToLower(strings.TrimSpace(app)) {
	case "discord":
		if runtime.GOOS != "linux" {
			return browserAppSpec{}, fmt.Errorf("discord launch is only supported for Linux flatpak installs")
		}
		return browserAppSpec{
			name:     "discord",
			command:  "flatpak",
			args:     []string{"run", "--command=com.discordapp.Discord", "com.discordapp.Discord", "--remote-debugging-port=" + strconv.Itoa(port)},
			stopPath: "flatpak",
			stopArgs: []string{"kill", "com.discordapp.Discord"},
		}, nil
	default:
		return browserAppSpec{}, fmt.Errorf("app must be one of: discord")
	}
}

func waitForDevTools(ctx context.Context, baseURL string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = defaultDevToolsWaitTimeout
	}
	deadline := time.Now().Add(timeout)
	client := http.Client{Timeout: 750 * time.Millisecond}
	endpoint := strings.TrimRight(baseURL, "/") + "/json/version"
	var lastErr error
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			lastErr = fmt.Errorf("DevTools endpoint returned HTTP %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("DevTools endpoint %s was not ready after %dms: %w", endpoint, timeout.Milliseconds(), lastErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}

type ExecBrowserAppRunner struct{}

func (ExecBrowserAppRunner) Run(ctx context.Context, path string, args []string, env []string, timeout time.Duration) (CommandResult, error) {
	return ExecRunner{}.Run(ctx, path, args, env, timeout)
}

func (ExecBrowserAppRunner) StartDetached(_ context.Context, path string, args []string, env []string) (int, error) {
	if strings.TrimSpace(path) == "" {
		return 0, errors.New("launch command is required")
	}
	cmd := exec.Command(path, args...)
	cmd.Env = mergeEnv(os.Environ(), env)
	configureDetachedProcess(cmd)
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	if err := cmd.Process.Release(); err != nil {
		return pid, err
	}
	return pid, nil
}
