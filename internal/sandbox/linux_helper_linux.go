//go:build linux

package sandbox

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
)

func RunLinuxSandboxHelper(args []string, stderr io.Writer) int {
	config, err := ParseLinuxSandboxHelperArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, LinuxSandboxHelperName+": "+err.Error())
		return 2
	}
	if config.ApplySeccompThenExec {
		return runLinuxSandboxInnerStage(config, stderr)
	}
	if config.UseLandlock {
		fmt.Fprintln(stderr, LinuxSandboxHelperName+": Landlock helper mode is not implemented yet")
		return 125
	}
	helperPath, err := os.Executable()
	if err != nil {
		fmt.Fprintln(stderr, LinuxSandboxHelperName+": resolve helper path: "+err.Error())
		return 125
	}
	bwrapArgs, err := BuildLinuxSandboxBwrapArgs(LinuxSandboxBwrapOptions{
		Config:     config,
		HelperPath: helperPath,
	})
	if err != nil {
		fmt.Fprintln(stderr, LinuxSandboxHelperName+": "+err.Error())
		return 125
	}
	bwrapPath, err := findBubblewrapExecutable()
	if err != nil {
		fmt.Fprintln(stderr, LinuxSandboxHelperName+": bubblewrap is not available: "+err.Error())
		return 125
	}
	if err := syscall.Exec(bwrapPath, append([]string{"bwrap"}, bwrapArgs...), os.Environ()); err != nil {
		fmt.Fprintln(stderr, LinuxSandboxHelperName+": exec bubblewrap: "+err.Error())
		return 126
	}
	return 126
}

func findBubblewrapExecutable() (string, error) {
	if path, err := exec.LookPath("bwrap"); err == nil && path != "" {
		return path, nil
	}
	for _, candidate := range []string{"/usr/bin/bwrap", "/bin/bwrap"} {
		if executableRegularFile(candidate) {
			return candidate, nil
		}
	}
	return "", exec.ErrNotFound
}

func runLinuxSandboxInnerStage(config LinuxSandboxHelperConfig, stderr io.Writer) int {
	if config.UseLandlock {
		fmt.Fprintln(stderr, LinuxSandboxHelperName+": inner seccomp stage is incompatible with Landlock mode")
		return 2
	}
	if config.AllowNetworkForProxy && config.ProxyRouteSpec == "" {
		fmt.Fprintln(stderr, LinuxSandboxHelperName+": proxy networking requires --proxy-route-spec")
		return 125
	}
	if err := ApplyUnixSocketBlock(); err != nil {
		fmt.Fprintln(stderr, LinuxSandboxHelperName+": apply seccomp: "+err.Error())
		return 126
	}
	binary, err := exec.LookPath(config.Command[0])
	if err != nil {
		fmt.Fprintln(stderr, LinuxSandboxHelperName+": "+err.Error())
		return 127
	}
	if err := syscall.Exec(binary, config.Command, os.Environ()); err != nil {
		fmt.Fprintln(stderr, LinuxSandboxHelperName+": exec command: "+err.Error())
		return 126
	}
	return 126
}
