package sandbox

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

const LinuxSandboxHelperName = "zero-linux-sandbox"

type LinuxSandboxCommandArgsOptions struct {
	SandboxPolicyCWD     string
	CommandCWD           string
	PermissionProfile    PermissionProfile
	UseLandlock          bool
	ApplySeccompThenExec bool
	AllowNetworkForProxy bool
	ProxyRouteSpec       string
	NoProc               bool
	Command              []string
}

type LinuxSandboxHelperConfig struct {
	SandboxPolicyCWD     string
	CommandCWD           string
	PermissionProfile    PermissionProfile
	UseLandlock          bool
	ApplySeccompThenExec bool
	AllowNetworkForProxy bool
	ProxyRouteSpec       string
	NoProc               bool
	Command              []string
}

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
	if options.AllowNetworkForProxy {
		args = append(args, "--allow-network-for-proxy")
	}
	if route := strings.TrimSpace(options.ProxyRouteSpec); route != "" {
		args = append(args, "--proxy-route-spec", route)
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
	flags.BoolVar(&config.AllowNetworkForProxy, "allow-network-for-proxy", false, "allow proxy-routed network")
	flags.StringVar(&config.ProxyRouteSpec, "proxy-route-spec", "", "proxy route spec")
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

func RunLinuxSandboxHelper(args []string, stderr io.Writer) int {
	if _, err := ParseLinuxSandboxHelperArgs(args); err != nil {
		fmt.Fprintln(stderr, LinuxSandboxHelperName+": "+err.Error())
		return 2
	}
	fmt.Fprintln(stderr, LinuxSandboxHelperName+": native Linux helper enforcement is not implemented yet")
	return 125
}
