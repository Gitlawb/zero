package localcontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	DefaultBrowserDriver  = "agent-browser"
	DefaultDesktopDriver  = "cua-driver"
	DefaultTerminalDriver = "tuistory"
	DefaultTimeout        = 30 * time.Second
	EnvHelperManifest     = "ZERO_LOCAL_CONTROL_HELPERS"
)

type HelperOptions struct {
	Enabled         bool
	Driver          string
	HelperPath      string
	ConfigPath      string
	DisabledMessage string
	MissingHint     string
	Timeout         time.Duration
	Runner          CommandRunner
}

type BrowserOptions = HelperOptions
type DesktopOptions = HelperOptions
type TerminalOptions = HelperOptions

type CommandResult struct {
	Path     string
	Args     []string
	Stdout   string
	Stderr   string
	ExitCode int
}

func (result CommandResult) Output() string {
	stdout := strings.TrimRight(result.Stdout, "\n")
	stderr := strings.TrimRight(result.Stderr, "\n")
	switch {
	case stdout == "" && stderr == "":
		return ""
	case stdout == "":
		return stderr
	case stderr == "":
		return stdout
	default:
		return stdout + "\n" + stderr
	}
}

type CommandRunner interface {
	Run(ctx context.Context, path string, args []string, env []string, timeout time.Duration) (CommandResult, error)
}

type ResolvedHelper struct {
	Command    string
	PrefixArgs []string
	Env        []string
}

type Helper struct {
	options HelperOptions
}

type Browser struct {
	helper Helper
}

type Desktop struct {
	helper Helper
}

type Terminal struct {
	helper Helper
}

func NewHelper(options HelperOptions) Helper {
	if options.Timeout <= 0 {
		options.Timeout = DefaultTimeout
	}
	if options.Runner == nil {
		options.Runner = ExecRunner{}
	}
	return Helper{options: options}
}

func NewBrowser(options BrowserOptions) Browser {
	if strings.TrimSpace(options.Driver) == "" {
		options.Driver = DefaultBrowserDriver
	}
	options.ConfigPath = "localControl.browser.helperPath"
	options.DisabledMessage = "local browser control is disabled by user config"
	options.MissingHint = "install the agent-browser package or set localControl.browser.helperPath"
	return Browser{helper: NewHelper(options)}
}

func NewDesktop(options DesktopOptions) Desktop {
	if strings.TrimSpace(options.Driver) == "" {
		options.Driver = DefaultDesktopDriver
	}
	options.ConfigPath = "localControl.desktop.helperPath"
	options.DisabledMessage = "local desktop control is disabled; enable localControl.desktop.enabled in user config"
	options.MissingHint = "install cua-driver or set localControl.desktop.helperPath"
	return Desktop{helper: NewHelper(options)}
}

func NewTerminal(options TerminalOptions) Terminal {
	if strings.TrimSpace(options.Driver) == "" {
		options.Driver = DefaultTerminalDriver
	}
	options.ConfigPath = "localControl.terminal.helperPath"
	options.DisabledMessage = "local terminal control is disabled by user config"
	options.MissingHint = "install the tuistory package or set localControl.terminal.helperPath"
	return Terminal{helper: NewHelper(options)}
}

func (helper Helper) Enabled() bool {
	return helper.options.Enabled
}

func (helper Helper) Run(ctx context.Context, args ...string) (CommandResult, error) {
	if !helper.options.Enabled {
		message := strings.TrimSpace(helper.options.DisabledMessage)
		if message == "" {
			message = "local control helper is disabled"
		}
		return CommandResult{}, errors.New(message)
	}
	resolved, err := helper.resolvedHelper()
	if err != nil {
		return CommandResult{}, err
	}
	runArgs := append([]string{}, resolved.PrefixArgs...)
	runArgs = append(runArgs, args...)
	return helper.options.Runner.Run(ctx, resolved.Command, runArgs, resolved.Env, helper.options.Timeout)
}

func (helper Helper) resolvedHelper() (ResolvedHelper, error) {
	label := helper.helperLabel()
	if helperPath := strings.TrimSpace(helper.options.HelperPath); helperPath != "" {
		if !filepath.IsAbs(helperPath) {
			abs, err := filepath.Abs(helperPath)
			if err != nil {
				return ResolvedHelper{}, fmt.Errorf("resolve %s helper path: %w", label, err)
			}
			helperPath = abs
		}
		if err := validateHelperFile(label, helperPath); err != nil {
			return ResolvedHelper{}, err
		}
		return ResolvedHelper{Command: helperPath}, nil
	}

	driver := strings.TrimSpace(helper.options.Driver)
	if driver == "" {
		driver = DefaultBrowserDriver
	}
	if resolved, ok, err := helperFromManifest(driver); ok || err != nil {
		return resolved, err
	}
	if resolved, ok, err := adjacentHelper(driver); ok || err != nil {
		return resolved, err
	}
	path, err := exec.LookPath(driver)
	if err != nil {
		hint := strings.TrimSpace(helper.options.MissingHint)
		if hint == "" {
			hint = "install it or configure the helper path"
		}
		return ResolvedHelper{}, fmt.Errorf("%s was not found on PATH; %s", driver, hint)
	}
	return ResolvedHelper{Command: path}, nil
}

func (helper Helper) helperLabel() string {
	driver := strings.TrimSpace(helper.options.Driver)
	if driver == "" {
		return "local control"
	}
	return driver
}

func (browser Browser) Enabled() bool {
	return browser.helper.Enabled()
}

func (browser Browser) Run(ctx context.Context, args ...string) (CommandResult, error) {
	return browser.helper.Run(ctx, args...)
}

func (desktop Desktop) Run(ctx context.Context, args ...string) (CommandResult, error) {
	return desktop.helper.Run(ctx, args...)
}

func (desktop Desktop) Enabled() bool {
	return desktop.helper.Enabled()
}

func (terminal Terminal) Run(ctx context.Context, args ...string) (CommandResult, error) {
	return terminal.helper.Run(ctx, args...)
}

func (terminal Terminal) Enabled() bool {
	return terminal.helper.Enabled()
}

type ExecRunner struct{}

var executablePath = os.Executable

func (ExecRunner) Run(ctx context.Context, path string, args []string, env []string, timeout time.Duration) (CommandResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, path, args...)
	cmd.Env = mergeEnv(os.Environ(), env)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := CommandResult{
		Path:   path,
		Args:   append([]string(nil), args...),
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	} else if err != nil {
		result.ExitCode = -1
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return result, fmt.Errorf("local control helper timed out after %dms", timeout.Milliseconds())
	}
	if err != nil {
		return result, err
	}
	return result, nil
}

type helperManifest struct {
	Version int                      `json:"version"`
	Helpers map[string]manifestEntry `json:"helpers"`
}

type manifestEntry struct {
	Command     string            `json:"command"`
	PrefixArgs  []string          `json:"prefixArgs"`
	PathPrepend []string          `json:"pathPrepend"`
	Env         map[string]string `json:"env"`
}

func helperFromManifest(driver string) (ResolvedHelper, bool, error) {
	raw := strings.TrimSpace(os.Getenv(EnvHelperManifest))
	if raw == "" {
		return ResolvedHelper{}, false, nil
	}
	manifest, err := parseHelperManifest(raw)
	if err != nil {
		return ResolvedHelper{}, true, err
	}
	entry, ok := manifest.Helpers[driver]
	if !ok {
		return ResolvedHelper{}, false, nil
	}
	command := strings.TrimSpace(entry.Command)
	if command == "" {
		return ResolvedHelper{}, true, fmt.Errorf("%s entry in %s is missing command", driver, EnvHelperManifest)
	}
	if filepath.IsAbs(command) {
		if err := validateHelperFile(driver, command); err != nil {
			return ResolvedHelper{}, true, err
		}
	}
	return ResolvedHelper{
		Command:    command,
		PrefixArgs: cleanStringSlice(entry.PrefixArgs),
		Env:        manifestEnv(entry),
	}, true, nil
}

func parseHelperManifest(raw string) (helperManifest, error) {
	var manifest helperManifest
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return helperManifest{}, fmt.Errorf("invalid %s JSON: %w", EnvHelperManifest, err)
	}
	if manifest.Helpers == nil {
		manifest.Helpers = map[string]manifestEntry{}
	}
	return manifest, nil
}

func validateHelperFile(label string, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%s helper not found at %s: %w", label, path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s helper path is a directory: %s", label, path)
	}
	return nil
}

func adjacentHelper(driver string) (ResolvedHelper, bool, error) {
	executable, err := executablePath()
	if err != nil {
		return ResolvedHelper{}, false, nil
	}
	executableDir := filepath.Dir(executable)
	for _, dir := range []string{
		executableDir,
		filepath.Join(executableDir, "helpers"),
		filepath.Join(executableDir, "helpers", "node_modules", ".bin"),
		filepath.Join(executableDir, "node_modules", ".bin"),
	} {
		for _, name := range helperExecutableNames(driver) {
			path := filepath.Join(dir, name)
			if err := validateHelperFile(driver, path); err == nil {
				return helperCommandForPath(path), true, nil
			}
		}
	}
	return ResolvedHelper{}, false, nil
}

func helperExecutableNames(driver string) []string {
	if runtime.GOOS == "windows" {
		return []string{driver + ".cmd", driver + ".exe", driver}
	}
	return []string{driver}
}

func helperCommandForPath(path string) ResolvedHelper {
	if runtime.GOOS == "windows" && strings.EqualFold(filepath.Ext(path), ".cmd") {
		command := os.Getenv("ComSpec")
		if strings.TrimSpace(command) == "" {
			command = "cmd.exe"
		}
		return ResolvedHelper{
			Command:    command,
			PrefixArgs: []string{"/d", "/s", "/c", `"` + strings.ReplaceAll(path, `"`, `""`) + `"`},
		}
	}
	return ResolvedHelper{Command: path}
}

func manifestEnv(entry manifestEntry) []string {
	env := map[string]string{}
	for key, value := range entry.Env {
		key = strings.TrimSpace(key)
		if key != "" {
			env[key] = value
		}
	}
	pathPrepend := cleanStringSlice(entry.PathPrepend)
	if len(pathPrepend) > 0 {
		basePath := env["PATH"]
		if basePath == "" {
			basePath = os.Getenv("PATH")
		}
		prefix := strings.Join(pathPrepend, string(os.PathListSeparator))
		if basePath != "" {
			env["PATH"] = prefix + string(os.PathListSeparator) + basePath
		} else {
			env["PATH"] = prefix
		}
	}
	return envMapToList(env)
}

func normalizeEnvKey(key string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(key)
	}
	return key
}

func mergeEnv(base []string, overlay []string) []string {
	if len(overlay) == 0 {
		return base
	}
	merged := append([]string{}, base...)
	index := map[string]int{}
	for i, item := range merged {
		if key, _, ok := strings.Cut(item, "="); ok {
			index[normalizeEnvKey(key)] = i
		}
	}
	for _, item := range overlay {
		key, _, ok := strings.Cut(item, "=")
		if !ok || key == "" {
			continue
		}
		normalized := normalizeEnvKey(key)
		if existing, ok := index[normalized]; ok {
			merged[existing] = item
		} else {
			index[normalized] = len(merged)
			merged = append(merged, item)
		}
	}
	return merged
}

func cleanStringSlice(values []string) []string {
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			cleaned = append(cleaned, value)
		}
	}
	return cleaned
}

func envMapToList(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		values = append(values, key+"="+env[key])
	}
	return values
}
