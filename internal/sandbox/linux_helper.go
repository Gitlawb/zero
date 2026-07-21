package sandbox

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const LinuxSandboxHelperName = "zero-linux-sandbox"

const linuxSandboxBackendEnv = BackendLinuxBwrap

type LinuxSandboxCommandArgsOptions struct {
	SandboxPolicyCWD     string
	CommandCWD           string
	PermissionProfile    PermissionProfile
	UseLandlock          bool
	ApplySeccompThenExec bool
	BlockUnixSockets     bool
	NoProc               bool
	Command              []string
}

type LinuxSandboxHelperConfig struct {
	SandboxPolicyCWD     string
	CommandCWD           string
	PermissionProfile    PermissionProfile
	UseLandlock          bool
	ApplySeccompThenExec bool
	BlockUnixSockets     bool
	NoProc               bool
	Command              []string
}

type LinuxSandboxHelperCommand struct {
	Name       string
	ArgsPrefix []string
	Dir        string
}

type LinuxSandboxBwrapOptions struct {
	Config     LinuxSandboxHelperConfig
	HelperPath string
}

var linuxSandboxHelperCommand = findLinuxSandboxHelperCommand

func BuildLinuxSandboxCommandArgs(options LinuxSandboxCommandArgsOptions) ([]string, error) {
	sandboxPolicyCWD := strings.TrimSpace(options.SandboxPolicyCWD)
	if sandboxPolicyCWD == "" {
		return nil, errors.New("linux sandbox helper requires sandbox policy cwd")
	}
	commandCWD := strings.TrimSpace(options.CommandCWD)
	if commandCWD == "" {
		commandCWD = sandboxPolicyCWD
	}
	if len(options.Command) == 0 {
		return nil, errors.New("linux sandbox helper requires command")
	}
	profileJSON, err := json.Marshal(options.PermissionProfile)
	if err != nil {
		return nil, fmt.Errorf("marshal linux sandbox permission profile: %w", err)
	}
	args := []string{
		"--sandbox-policy-cwd", sandboxPolicyCWD,
		"--command-cwd", commandCWD,
		"--permission-profile", string(profileJSON),
	}
	if options.UseLandlock {
		args = append(args, "--use-landlock")
	}
	if options.ApplySeccompThenExec {
		args = append(args, "--apply-seccomp-then-exec")
	}
	if options.BlockUnixSockets {
		args = append(args, "--block-unix-sockets")
	}
	if options.NoProc {
		args = append(args, "--no-proc")
	}
	args = append(args, "--")
	args = append(args, options.Command...)
	return args, nil
}

func ParseLinuxSandboxHelperArgs(args []string) (LinuxSandboxHelperConfig, error) {
	var config LinuxSandboxHelperConfig
	var profileJSON string
	flags := flag.NewFlagSet(LinuxSandboxHelperName, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&config.SandboxPolicyCWD, "sandbox-policy-cwd", "", "sandbox policy cwd")
	flags.StringVar(&config.CommandCWD, "command-cwd", "", "command cwd")
	flags.StringVar(&profileJSON, "permission-profile", "", "permission profile JSON")
	flags.BoolVar(&config.UseLandlock, "use-landlock", false, "use Landlock backend")
	flags.BoolVar(&config.ApplySeccompThenExec, "apply-seccomp-then-exec", false, "apply seccomp before exec")
	flags.BoolVar(&config.BlockUnixSockets, "block-unix-sockets", false, "block AF_UNIX sockets before exec")
	flags.BoolVar(&config.NoProc, "no-proc", false, "skip proc mount")
	if err := flags.Parse(args); err != nil {
		return LinuxSandboxHelperConfig{}, err
	}
	config.SandboxPolicyCWD = strings.TrimSpace(config.SandboxPolicyCWD)
	if config.SandboxPolicyCWD == "" {
		return LinuxSandboxHelperConfig{}, errors.New("missing --sandbox-policy-cwd")
	}
	config.CommandCWD = strings.TrimSpace(config.CommandCWD)
	if config.CommandCWD == "" {
		config.CommandCWD = config.SandboxPolicyCWD
	}
	profileJSON = strings.TrimSpace(profileJSON)
	if profileJSON == "" {
		return LinuxSandboxHelperConfig{}, errors.New("missing --permission-profile")
	}
	if err := json.Unmarshal([]byte(profileJSON), &config.PermissionProfile); err != nil {
		return LinuxSandboxHelperConfig{}, fmt.Errorf("invalid --permission-profile: %w", err)
	}
	config.Command = flags.Args()
	if len(config.Command) == 0 {
		return LinuxSandboxHelperConfig{}, errors.New("missing command after --")
	}
	return config, nil
}

func BuildLinuxSandboxBwrapArgs(options LinuxSandboxBwrapOptions) ([]string, error) {
	config := options.Config
	if config.ApplySeccompThenExec {
		return nil, errors.New("inner seccomp stage cannot be wrapped by bubblewrap again")
	}
	if config.UseLandlock {
		return nil, errors.New("linux landlock helper mode is not implemented yet")
	}
	helperPath := strings.TrimSpace(options.HelperPath)
	if helperPath == "" {
		return nil, errors.New("linux sandbox helper path is required")
	}
	commandCWD := strings.TrimSpace(config.CommandCWD)
	if commandCWD == "" {
		commandCWD = config.SandboxPolicyCWD
	}
	innerArgs, err := BuildLinuxSandboxCommandArgs(LinuxSandboxCommandArgsOptions{
		SandboxPolicyCWD:     config.SandboxPolicyCWD,
		CommandCWD:           commandCWD,
		PermissionProfile:    config.PermissionProfile,
		ApplySeccompThenExec: true,
		BlockUnixSockets:     config.BlockUnixSockets,
		NoProc:               config.NoProc,
		Command:              config.Command,
	})
	if err != nil {
		return nil, err
	}
	args := []string{
		"--new-session",
		"--die-with-parent",
	}
	filesystemArgs, err := linuxBwrapFilesystemArgs(config.PermissionProfile)
	if err != nil {
		return nil, err
	}
	args = append(args, filesystemArgs...)
	if pathExists(helperPath) {
		args = append(args, "--ro-bind", helperPath, helperPath)
	}
	args = append(args,
		"--unshare-user",
		"--unshare-pid",
	)
	// Keep IPC and UTS shared for compatibility with the host CLI environment;
	// network isolation is still applied below when the policy denies egress.
	if shouldUnshareLinuxNetwork(config.PermissionProfile.Network) {
		args = append(args, "--unshare-net")
	}
	if !config.NoProc {
		args = append(args, "--proc", "/proc")
	}
	args = append(args, "--chdir", commandCWD)
	for _, env := range linuxHelperSandboxEnvironmentOverrides(config.PermissionProfile) {
		key, value, ok := strings.Cut(env, "=")
		if ok {
			args = append(args, "--setenv", key, value)
		}
	}
	args = append(args, "--", helperPath)
	args = append(args, innerArgs...)
	return args, nil
}

func linuxBwrapFilesystemArgs(profile PermissionProfile) ([]string, error) {
	fs := profile.FileSystem
	if fs.Kind == FileSystemUnrestricted {
		// Disabled filesystem policy means no write jail: expose the host root
		// read-write, including the host /dev tree, rather than synthesizing a
		// restricted bubblewrap filesystem.
		args := []string{"--bind", "/", "/"}
		for _, root := range fs.WriteRoots {
			if pathExists(root.Root) {
				args = append(args, "--bind", root.Root, root.Root)
			}
		}
		return args, nil
	}

	args := []string{}
	if linuxProfileHasFullReadRoot(fs) {
		args = append(args, "--ro-bind", "/", "/", "--dev", "/dev")
	} else {
		args = append(args, "--tmpfs", "/", "--dev", "/dev")
		if fs.IncludePlatformRoots {
			for _, root := range linuxPlatformReadRoots() {
				args = append(args, "--ro-bind", root, root)
			}
		}
		for _, root := range fs.ReadRoots {
			if pathExists(root) {
				args = append(args, "--ro-bind", root, root)
			}
		}
	}
	if fs.AllowTemp {
		fs.WriteRoots = linuxWriteRootsWithTemp(fs)
	}
	for _, root := range linuxSortedWriteRoots(fs.WriteRoots) {
		if !pathExists(root.Root) {
			continue
		}
		args = append(args, "--bind", root.Root, root.Root)
		for _, subpath := range root.ReadOnlySubpaths {
			if !pathExists(subpath) {
				// Bubblewrap cannot mount over an absent child after binding a
				// writable host root without creating that child on the host. Do not
				// mutate the workspace merely to prepare the mount namespace.
				continue
			}
			var err error
			args, err = appendReadOnlyLinuxPathArgs(args, subpath)
			if err != nil {
				return nil, err
			}
		}
		for _, name := range root.ProtectedMetadataNames {
			path := filepath.Join(root.Root, name)
			if !pathExists(path) {
				// As above, an absent mount point cannot be represented without
				// either changing the host or making the writable root read-only.
				continue
			}
			var err error
			args, err = appendReadOnlyLinuxPathArgs(args, path)
			if err != nil {
				return nil, err
			}
		}
	}
	for _, path := range fs.DenyWrite {
		var err error
		args, err = appendReadOnlyLinuxPathArgs(args, path)
		if err != nil {
			return nil, err
		}
	}
	for _, path := range fs.DenyRead {
		var err error
		args, err = appendUnreadableLinuxPathArgs(args, path)
		if err != nil {
			return nil, err
		}
	}
	for _, path := range fs.DenyReadIfExists {
		if !pathExists(path) {
			// The read-all profile begins with a read-only host-root bind. Bwrap
			// cannot create a missing mount destination in that tree, and masking
			// its nearest existing parent could hide a workspace, HOME, or /tmp.
			continue
		}
		var err error
		args, err = appendUnreadableLinuxPathArgs(args, path)
		if err != nil {
			return nil, err
		}
	}
	return args, nil
}

func linuxWriteRootsWithTemp(fs FileSystemPolicy) []WritableRoot {
	roots := append([]WritableRoot{}, fs.WriteRoots...)
	for _, tempRoot := range defaultTempWriteRoots() {
		found := false
		for _, root := range roots {
			if filepath.Clean(root.Root) == filepath.Clean(tempRoot) {
				found = true
				break
			}
		}
		if !found {
			roots = append(roots, WritableRoot{Root: tempRoot})
		}
	}
	return roots
}

func linuxSortedWriteRoots(roots []WritableRoot) []WritableRoot {
	sorted := append([]WritableRoot{}, roots...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return pathDepth(sorted[i].Root) < pathDepth(sorted[j].Root)
	})
	return sorted
}

func pathDepth(path string) int {
	cleaned := filepath.Clean(path)
	if cleaned == "" || filepath.Dir(cleaned) == cleaned {
		return 0
	}
	trimmed := strings.Trim(cleaned, string(filepath.Separator))
	if trimmed == "" {
		return 0
	}
	return strings.Count(trimmed, string(filepath.Separator)) + 1
}

func linuxProfileHasFullReadRoot(fs FileSystemPolicy) bool {
	for _, root := range fs.ReadRoots {
		if filepath.Clean(root) == string(filepath.Separator) {
			return true
		}
	}
	return false
}

func linuxPlatformReadRoots() []string {
	candidates := []string{"/bin", "/sbin", "/usr", "/etc", "/lib", "/lib64", "/nix/store", "/run/current-system/sw"}
	roots := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if pathExists(candidate) {
			roots = append(roots, candidate)
		}
	}
	return roots
}

func appendReadOnlyLinuxPathArgs(args []string, path string) ([]string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return args, nil
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("cannot enforce Linux read-only path %q: %w", path, err)
	}
	return append(args, "--ro-bind", path, path), nil
}

func appendUnreadableLinuxPathArgs(args []string, path string) ([]string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return args, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("cannot enforce Linux deny-read path %q: %w", path, err)
	}
	if !info.IsDir() {
		return append(args, "--ro-bind", "/dev/null", path), nil
	}
	return append(args, "--perms", "000", "--tmpfs", path, "--remount-ro", path), nil
}

func shouldUnshareLinuxNetwork(policy NetworkPolicy) bool {
	return NormalizeNetworkMode(policy.Mode) == NetworkDeny
}

func linuxHelperSandboxEnvironment(profile PermissionProfile, base []string) []string {
	return upsertEnvList(base, linuxHelperSandboxEnvironmentOverrides(profile)...)
}

func linuxHelperSandboxEnvironmentOverrides(profile PermissionProfile) []string {
	return []string{
		EnvSandboxBackend + "=" + string(linuxSandboxBackendEnv),
		"ZERO_SANDBOX_NETWORK=" + string(profile.Network.Mode),
		EnvSandboxed + "=1",
	}
}

func pathExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func findLinuxSandboxHelperCommand() (LinuxSandboxHelperCommand, error) {
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), LinuxSandboxHelperName)
		if executableRegularFile(candidate) {
			return LinuxSandboxHelperCommand{Name: candidate}, nil
		}
	}
	if path, err := exec.LookPath(LinuxSandboxHelperName); err == nil && path != "" {
		return LinuxSandboxHelperCommand{Name: path}, nil
	}
	if root := linuxSandboxRepoRoot(); root != "" {
		mainPath := filepath.Join(root, "cmd", LinuxSandboxHelperName, "main.go")
		if _, err := os.Stat(mainPath); err == nil {
			if goPath, lookErr := exec.LookPath("go"); lookErr == nil && goPath != "" {
				return LinuxSandboxHelperCommand{
					Name:       goPath,
					ArgsPrefix: []string{"run", "./cmd/" + LinuxSandboxHelperName},
					Dir:        root,
				}, nil
			}
		}
	}
	return LinuxSandboxHelperCommand{}, errors.New("zero-linux-sandbox helper is not available")
}

func executableRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

func linuxSandboxRepoRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return ""
	}
	return root
}
