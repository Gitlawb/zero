package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

const WindowsSandboxCommandRunnerName = "zero-windows-command-runner.exe"

type WindowsSandboxLevel string

const (
	WindowsSandboxLevelRestrictedToken WindowsSandboxLevel = "restricted-token"
	WindowsSandboxLevelElevated        WindowsSandboxLevel = "elevated"
	WindowsSandboxLevelDisabled        WindowsSandboxLevel = "disabled"
)

type WindowsSandboxCommandArgsOptions struct {
	CommandCWD        string
	WorkspaceRoots    []string
	PermissionProfile PermissionProfile
	Env               []string
	SandboxLevel      WindowsSandboxLevel
	Command           []string
}

type WindowsSandboxCommandConfig struct {
	CommandCWD        string
	WorkspaceRoots    []string
	PermissionProfile PermissionProfile
	Env               map[string]string
	SandboxLevel      WindowsSandboxLevel
	Command           []string
}

func BuildWindowsSandboxCommandArgs(options WindowsSandboxCommandArgsOptions) ([]string, error) {
	commandCWD := strings.TrimSpace(options.CommandCWD)
	if commandCWD == "" {
		return nil, errors.New("windows sandbox command runner requires command cwd")
	}
	if len(options.Command) == 0 {
		return nil, errors.New("windows sandbox command runner requires command")
	}
	level := options.SandboxLevel
	if level == "" {
		level = WindowsSandboxLevelRestrictedToken
	}
	if !validWindowsSandboxLevel(level) {
		return nil, fmt.Errorf("unsupported windows sandbox level %q", level)
	}
	workspaceRoots := trimNonEmptyStrings(options.WorkspaceRoots)
	if len(workspaceRoots) == 0 {
		workspaceRoots = []string{commandCWD}
	}
	profileJSON, err := json.Marshal(options.PermissionProfile)
	if err != nil {
		return nil, fmt.Errorf("marshal windows sandbox permission profile: %w", err)
	}
	envJSON, err := json.Marshal(envListToMap(options.Env))
	if err != nil {
		return nil, fmt.Errorf("marshal windows sandbox environment: %w", err)
	}
	args := []string{
		"--command-cwd", commandCWD,
		"--permission-profile", string(profileJSON),
		"--env-json", string(envJSON),
		"--windows-sandbox-level", string(level),
	}
	for _, root := range workspaceRoots {
		args = append(args, "--workspace-root", root)
	}
	args = append(args, "--")
	args = append(args, options.Command...)
	return args, nil
}

func ParseWindowsSandboxCommandArgs(args []string) (WindowsSandboxCommandConfig, error) {
	var config WindowsSandboxCommandConfig
	var profileJSON string
	var envJSON string
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--":
			config.Command = cloneStrings(args[index+1:])
			index = len(args)
		case "--command-cwd":
			value, next, err := nextWindowsSandboxFlagValue(args, index)
			if err != nil {
				return WindowsSandboxCommandConfig{}, err
			}
			config.CommandCWD = strings.TrimSpace(value)
			index = next
		case "--workspace-root":
			value, next, err := nextWindowsSandboxFlagValue(args, index)
			if err != nil {
				return WindowsSandboxCommandConfig{}, err
			}
			if root := strings.TrimSpace(value); root != "" {
				config.WorkspaceRoots = append(config.WorkspaceRoots, root)
			}
			index = next
		case "--permission-profile":
			value, next, err := nextWindowsSandboxFlagValue(args, index)
			if err != nil {
				return WindowsSandboxCommandConfig{}, err
			}
			profileJSON = strings.TrimSpace(value)
			index = next
		case "--env-json":
			value, next, err := nextWindowsSandboxFlagValue(args, index)
			if err != nil {
				return WindowsSandboxCommandConfig{}, err
			}
			envJSON = strings.TrimSpace(value)
			index = next
		case "--windows-sandbox-level":
			value, next, err := nextWindowsSandboxFlagValue(args, index)
			if err != nil {
				return WindowsSandboxCommandConfig{}, err
			}
			config.SandboxLevel = WindowsSandboxLevel(strings.TrimSpace(value))
			index = next
		default:
			return WindowsSandboxCommandConfig{}, fmt.Errorf("unknown windows sandbox runner flag %q", arg)
		}
	}
	if config.CommandCWD == "" {
		return WindowsSandboxCommandConfig{}, errors.New("missing --command-cwd")
	}
	if len(config.WorkspaceRoots) == 0 {
		config.WorkspaceRoots = []string{config.CommandCWD}
	}
	if profileJSON == "" {
		return WindowsSandboxCommandConfig{}, errors.New("missing --permission-profile")
	}
	if err := json.Unmarshal([]byte(profileJSON), &config.PermissionProfile); err != nil {
		return WindowsSandboxCommandConfig{}, fmt.Errorf("invalid --permission-profile: %w", err)
	}
	if envJSON == "" {
		return WindowsSandboxCommandConfig{}, errors.New("missing --env-json")
	}
	if err := json.Unmarshal([]byte(envJSON), &config.Env); err != nil {
		return WindowsSandboxCommandConfig{}, fmt.Errorf("invalid --env-json: %w", err)
	}
	if config.SandboxLevel == "" {
		config.SandboxLevel = WindowsSandboxLevelRestrictedToken
	}
	if !validWindowsSandboxLevel(config.SandboxLevel) {
		return WindowsSandboxCommandConfig{}, fmt.Errorf("unsupported windows sandbox level %q", config.SandboxLevel)
	}
	if len(config.Command) == 0 {
		return WindowsSandboxCommandConfig{}, errors.New("missing command after --")
	}
	return config, nil
}

func windowsRestrictedTokenCommandPlan(execRequest SandboxExecutionRequest, policy Policy) (CommandPlan, error) {
	spec := execRequest.Command
	childEnv := windowsSandboxChildEnv(spec.Env, policy, execRequest.WorkspaceRoot)
	args, err := BuildWindowsSandboxCommandArgs(WindowsSandboxCommandArgsOptions{
		CommandCWD:        spec.Dir,
		WorkspaceRoots:    []string{execRequest.WorkspaceRoot},
		PermissionProfile: execRequest.PermissionProfile,
		Env:               childEnv,
		SandboxLevel:      WindowsSandboxLevelRestrictedToken,
		Command:           append([]string{spec.Name}, spec.Args...),
	})
	if err != nil {
		return CommandPlan{}, err
	}
	return withSandboxExecutionMetadata(CommandPlan{
		Backend:           execRequest.Backend,
		TargetBackend:     execRequest.TargetBackend,
		WorkspaceRoot:     execRequest.WorkspaceRoot,
		Policy:            policy,
		Wrapped:           true,
		SandboxEnvMarkers: execRequest.SandboxEnvMarkers,
		EnforcementLevel:  execRequest.EnforcementLevel,
		Name:              execRequest.Backend.Executable,
		Args:              args,
		Dir:               spec.Dir,
		Env:               childEnv,
		SandboxDir:        spec.Dir,
	}, execRequest), nil
}

func windowsSandboxChildEnv(specEnv []string, policy Policy, workspaceRoot string) []string {
	var env []string
	if specEnv != nil {
		env = cloneStrings(specEnv)
	} else {
		env = append(env, os.Environ()...)
	}
	env = upsertEnvList(env,
		"HOME="+workspaceRoot,
		"PATH="+firstEnv("PATH", defaultPath()),
		"TERM="+firstEnv("TERM", "dumb"),
		EnvSandboxBackend+"="+string(BackendWindowsRestrictedToken),
		"ZERO_SANDBOX_NETWORK="+string(policy.Network),
		EnvSandboxed+"=1",
		"COMSPEC="+firstEnv("COMSPEC", "cmd.exe"),
		"SystemRoot="+firstEnv("SystemRoot", `C:\Windows`),
		"WINDIR="+firstEnv("WINDIR", `C:\Windows`),
	)
	return env
}

func upsertEnvList(env []string, values ...string) []string {
	out := cloneStrings(env)
	for _, value := range values {
		key, _, ok := strings.Cut(value, "=")
		if !ok || strings.TrimSpace(key) == "" {
			continue
		}
		replaced := false
		for index, existing := range out {
			existingKey, _, existingOK := strings.Cut(existing, "=")
			if existingOK && strings.EqualFold(existingKey, key) {
				out[index] = value
				replaced = true
			}
		}
		if !replaced {
			out = append(out, value)
		}
	}
	return out
}

func envListToMap(env []string) map[string]string {
	out := map[string]string{}
	for _, value := range env {
		key, envValue, ok := strings.Cut(value, "=")
		if ok && strings.TrimSpace(key) != "" {
			out[key] = envValue
		}
	}
	return out
}

func trimNonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func nextWindowsSandboxFlagValue(args []string, index int) (string, int, error) {
	if index+1 >= len(args) {
		return "", index, fmt.Errorf("missing value for %s", args[index])
	}
	value := args[index+1]
	if value == "--" || strings.HasPrefix(value, "--") {
		return "", index, fmt.Errorf("missing value for %s", args[index])
	}
	return value, index + 1, nil
}

func validWindowsSandboxLevel(level WindowsSandboxLevel) bool {
	switch level {
	case WindowsSandboxLevelRestrictedToken, WindowsSandboxLevelElevated, WindowsSandboxLevelDisabled:
		return true
	default:
		return false
	}
}
