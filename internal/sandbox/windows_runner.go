package sandbox

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const WindowsSandboxCommandRunnerName = "zero-windows-command-runner.exe"

const windowsCapabilitySIDSchemaVersion = 1

func findWindowsSandboxCommandRunner(lookup func(string) (string, error)) string {
	return findAdjacentOrPathExecutable(WindowsSandboxCommandRunnerName, lookup)
}

func findAdjacentOrPathExecutable(name string, lookup func(string) (string, error)) string {
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), name)
		if regularFile(candidate) {
			return candidate
		}
	}
	if lookup != nil {
		if path, err := lookup(name); err == nil && path != "" {
			return path
		}
	}
	return ""
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

type WindowsSandboxLevel string

const (
	WindowsSandboxLevelRestrictedToken WindowsSandboxLevel = "restricted-token"
	WindowsSandboxLevelElevated        WindowsSandboxLevel = "elevated"
	WindowsSandboxLevelDisabled        WindowsSandboxLevel = "disabled"
)

type WindowsSandboxCommandArgsOptions struct {
	SandboxHome       string
	CommandCWD        string
	WorkspaceRoots    []string
	PermissionProfile PermissionProfile
	Env               []string
	SandboxLevel      WindowsSandboxLevel
	Command           []string
}

type WindowsSandboxCommandConfig struct {
	SandboxHome       string
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
	sandboxHome := strings.TrimSpace(options.SandboxHome)
	if sandboxHome == "" {
		var err error
		sandboxHome, err = ResolveWindowsSandboxHome(nil)
		if err != nil {
			return nil, err
		}
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
		"--sandbox-home", sandboxHome,
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
		case "--sandbox-home":
			value, next, err := nextWindowsSandboxFlagValue(args, index)
			if err != nil {
				return WindowsSandboxCommandConfig{}, err
			}
			config.SandboxHome = strings.TrimSpace(value)
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
	if config.SandboxHome == "" {
		return WindowsSandboxCommandConfig{}, errors.New("missing --sandbox-home")
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
	var sandboxHomeEnv map[string]string
	if spec.Env != nil {
		sandboxHomeEnv = envListToMap(spec.Env)
	}
	sandboxHome, err := ResolveWindowsSandboxHome(sandboxHomeEnv)
	if err != nil {
		return CommandPlan{}, err
	}
	childEnv := windowsSandboxChildEnv(spec.Env, policy, execRequest.WorkspaceRoot)
	args, err := BuildWindowsSandboxCommandArgs(WindowsSandboxCommandArgsOptions{
		SandboxHome:       sandboxHome,
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

type WindowsCapabilitySIDs struct {
	SchemaVersion      int               `json:"schemaVersion"`
	ReadOnly           string            `json:"readOnly"`
	WorkspaceByRoot    map[string]string `json:"workspaceByRoot,omitempty"`
	WritableRootByPath map[string]string `json:"writableRootByPath,omitempty"`
}

func ResolveWindowsSandboxHome(env map[string]string) (string, error) {
	if override := strings.TrimSpace(envValue(env, "ZERO_WINDOWS_SANDBOX_HOME")); override != "" {
		if filepath.IsAbs(override) {
			return filepath.Clean(override), nil
		}
		return filepath.Abs(override)
	}
	grantPath, err := ResolveGrantPath(env)
	if err != nil {
		return "", err
	}
	return filepath.Dir(grantPath), nil
}

func WindowsCapabilitySIDPath(sandboxHome string) string {
	return filepath.Join(filepath.Clean(sandboxHome), "windows-cap-sids.json")
}

func LoadOrCreateWindowsCapabilitySIDs(sandboxHome string) (WindowsCapabilitySIDs, error) {
	sandboxHome = strings.TrimSpace(sandboxHome)
	if sandboxHome == "" {
		return WindowsCapabilitySIDs{}, errors.New("windows sandbox home is required")
	}
	path := WindowsCapabilitySIDPath(sandboxHome)
	if bytes, err := os.ReadFile(path); err == nil {
		var caps WindowsCapabilitySIDs
		if err := json.Unmarshal(bytes, &caps); err != nil {
			return WindowsCapabilitySIDs{}, fmt.Errorf("parse windows capability SIDs %s: %w", path, err)
		}
		normalizeWindowsCapabilitySIDs(&caps)
		if caps.ReadOnly != "" {
			return caps, nil
		}
	} else if !os.IsNotExist(err) {
		return WindowsCapabilitySIDs{}, fmt.Errorf("read windows capability SIDs %s: %w", path, err)
	}
	caps := WindowsCapabilitySIDs{
		SchemaVersion:      windowsCapabilitySIDSchemaVersion,
		ReadOnly:           randomWindowsCapabilitySID(),
		WorkspaceByRoot:    map[string]string{},
		WritableRootByPath: map[string]string{},
	}
	if err := saveWindowsCapabilitySIDs(path, caps); err != nil {
		return WindowsCapabilitySIDs{}, err
	}
	return caps, nil
}

func WindowsWorkspaceCapabilitySID(sandboxHome string, root string) (string, error) {
	caps, err := LoadOrCreateWindowsCapabilitySIDs(sandboxHome)
	if err != nil {
		return "", err
	}
	key := windowsCapabilityPathKey(root)
	if key == "" {
		return "", errors.New("workspace root is required")
	}
	if sid := caps.WorkspaceByRoot[key]; sid != "" {
		return sid, nil
	}
	if caps.WorkspaceByRoot == nil {
		caps.WorkspaceByRoot = map[string]string{}
	}
	sid := randomWindowsCapabilitySID()
	caps.WorkspaceByRoot[key] = sid
	return sid, saveWindowsCapabilitySIDs(WindowsCapabilitySIDPath(sandboxHome), caps)
}

func WindowsWritableRootCapabilitySID(sandboxHome string, root string) (string, error) {
	caps, err := LoadOrCreateWindowsCapabilitySIDs(sandboxHome)
	if err != nil {
		return "", err
	}
	key := windowsCapabilityPathKey(root)
	if key == "" {
		return "", errors.New("writable root is required")
	}
	if sid := caps.WritableRootByPath[key]; sid != "" {
		return sid, nil
	}
	if caps.WritableRootByPath == nil {
		caps.WritableRootByPath = map[string]string{}
	}
	sid := randomWindowsCapabilitySID()
	caps.WritableRootByPath[key] = sid
	return sid, saveWindowsCapabilitySIDs(WindowsCapabilitySIDPath(sandboxHome), caps)
}

func WindowsCapabilitySIDsForConfig(config WindowsSandboxCommandConfig) ([]string, error) {
	if config.PermissionProfile.FileSystem.Kind != FileSystemRestricted {
		return nil, errors.New("windows sandbox requires a restricted filesystem permission profile")
	}
	if len(config.PermissionProfile.FileSystem.WriteRoots) == 0 {
		caps, err := LoadOrCreateWindowsCapabilitySIDs(config.SandboxHome)
		if err != nil {
			return nil, err
		}
		return []string{caps.ReadOnly}, nil
	}
	out := make([]string, 0, len(config.PermissionProfile.FileSystem.WriteRoots))
	for _, root := range config.PermissionProfile.FileSystem.WriteRoots {
		path := strings.TrimSpace(root.Root)
		if path == "" {
			continue
		}
		var (
			sid string
			err error
		)
		if windowsRootMatchesWorkspace(path, config.WorkspaceRoots) {
			sid, err = WindowsWorkspaceCapabilitySID(config.SandboxHome, path)
		} else {
			sid, err = WindowsWritableRootCapabilitySID(config.SandboxHome, path)
		}
		if err != nil {
			return nil, err
		}
		out = append(out, sid)
	}
	if len(out) == 0 {
		return nil, errors.New("windows sandbox has no writable capability roots")
	}
	return out, nil
}

func normalizeWindowsCapabilitySIDs(caps *WindowsCapabilitySIDs) {
	if caps.SchemaVersion == 0 {
		caps.SchemaVersion = windowsCapabilitySIDSchemaVersion
	}
	if caps.WorkspaceByRoot == nil {
		caps.WorkspaceByRoot = map[string]string{}
	}
	if caps.WritableRootByPath == nil {
		caps.WritableRootByPath = map[string]string{}
	}
}

func saveWindowsCapabilitySIDs(path string, caps WindowsCapabilitySIDs) error {
	normalizeWindowsCapabilitySIDs(&caps)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create windows capability SID dir: %w", err)
	}
	bytes, err := json.MarshalIndent(caps, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal windows capability SIDs: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".windows-cap-sids-*.tmp")
	if err != nil {
		return fmt.Errorf("create windows capability SID temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(bytes); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write windows capability SID temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close windows capability SID temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace windows capability SID file: %w", err)
	}
	return nil
}

func windowsCapabilityPathKey(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	path = filepath.Clean(path)
	return strings.ToLower(strings.ReplaceAll(path, "/", `\`))
}

func windowsRootMatchesWorkspace(root string, workspaceRoots []string) bool {
	rootKey := windowsCapabilityPathKey(root)
	if rootKey == "" {
		return false
	}
	for _, workspaceRoot := range workspaceRoots {
		if windowsCapabilityPathKey(workspaceRoot) == rootKey {
			return true
		}
	}
	return false
}

func randomWindowsCapabilitySID() string {
	var words [4]uint32
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		panic("generate windows capability SID entropy: " + err.Error())
	}
	for index := range words {
		words[index] = binary.LittleEndian.Uint32(bytes[index*4 : index*4+4])
		if words[index] == 0 {
			words[index] = uint32(index + 1)
		}
	}
	return fmt.Sprintf("S-1-5-21-%d-%d-%d-%d", words[0], words[1], words[2], words[3])
}
