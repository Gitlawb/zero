//go:build windows

package sandbox

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func runWindowsCommandAsUser(token windows.Token, config WindowsSandboxCommandConfig) (int, error) {
	if len(config.Command) == 0 {
		return 1, errorsNewWindowsProcess("missing command")
	}
	commandLine, err := windowsCommandLine(config.Command)
	if err != nil {
		return 1, err
	}
	commandLinePtr, err := windows.UTF16PtrFromString(commandLine)
	if err != nil {
		return 1, fmt.Errorf("encode command line: %w", err)
	}
	cwdPtr, err := windows.UTF16PtrFromString(config.CommandCWD)
	if err != nil {
		return 1, fmt.Errorf("encode command cwd: %w", err)
	}
	envBlock, err := windowsEnvironmentBlock(config.Env)
	if err != nil {
		return 1, err
	}
	desktopPtr, err := windows.UTF16PtrFromString(`winsta0\default`)
	if err != nil {
		return 1, fmt.Errorf("encode desktop: %w", err)
	}

	stdin := windows.Handle(os.Stdin.Fd())
	stdout := windows.Handle(os.Stdout.Fd())
	stderr := windows.Handle(os.Stderr.Fd())
	for _, handle := range []windows.Handle{stdin, stdout, stderr} {
		if err := windows.SetHandleInformation(handle, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
			return 1, fmt.Errorf("make stdio handle inheritable: %w", err)
		}
	}

	var startup windows.StartupInfo
	startup.Cb = uint32(unsafe.Sizeof(startup))
	startup.Desktop = desktopPtr
	startup.Flags = windows.STARTF_USESTDHANDLES
	startup.StdInput = stdin
	startup.StdOutput = stdout
	startup.StdErr = stderr
	var process windows.ProcessInformation
	envPtr := &envBlock[0]
	// Claim the report side channel BEFORE the launch. Publishing can fail, and
	// after CreateProcessAsUser succeeds a running child exists whether or not the
	// fact can be recorded; taking the file first moves that failure to a point
	// where there is still nothing to own.
	report, err := openWindowsExecutionReport(config.ExecutionReportPath)
	if err != nil {
		return 1, err
	}
	published := false
	defer func() { report.close(published) }()
	if err := windows.CreateProcessAsUser(
		token,
		nil,
		commandLinePtr,
		nil,
		nil,
		true,
		windows.CREATE_UNICODE_ENVIRONMENT,
		envPtr,
		cwdPtr,
		&startup,
		&process,
	); err != nil {
		return 1, fmt.Errorf("CreateProcessAsUser: %w", err)
	}
	defer windows.CloseHandle(process.Process)
	defer windows.CloseHandle(process.Thread)
	// THE TRANSITION ONLY THIS PROCESS CAN SEE. Everything above can fail with the
	// helper already running, and the parent's exec.Cmd.Process cannot tell those
	// failures apart from a real sandboxed launch. The restricted child exists as
	// of this line, so this is where the fact is published.
	//
	// OWNERSHIP OUTLIVES REPORTING. The child is runnable and may already be
	// making external side effects, so a failure to publish must not return from
	// here and leave it running with nobody waiting on it: the parent would read a
	// missing report, correctly conclude that no child launched, and be free to
	// start a second one alongside the first. Take it down and reap it, then
	// report the failure.
	if err := report.publish(true); err != nil {
		terminateAndReapWindowsChild(process.Process)
		return 1, fmt.Errorf("record sandboxed child launch: %w", err)
	}
	published = true
	if _, err := windows.WaitForSingleObject(process.Process, windows.INFINITE); err != nil {
		return 1, fmt.Errorf("wait for sandboxed process: %w", err)
	}
	var exitCode uint32
	if err := windows.GetExitCodeProcess(process.Process, &exitCode); err != nil {
		return 1, fmt.Errorf("read sandboxed process exit code: %w", err)
	}
	return int(exitCode), nil
}

func windowsCommandLine(args []string) (string, error) {
	if len(args) == 0 {
		return "", errorsNewWindowsProcess("missing command")
	}
	for _, arg := range args {
		if strings.ContainsRune(arg, 0) {
			return "", errorsNewWindowsProcess("command argument contains NUL")
		}
	}
	// Zero's cmd.exe shell invocation needs its final element (the shell
	// command text) reaching cmd.exe raw, not escaped like a normal argv
	// element: see WindowsShellCommandLine for why. Every other command this
	// runner might launch is escaped normally below.
	if commandLine, ok := windowsShellCommandLineFromArgs(args); ok {
		return commandLine, nil
	}
	out := make([]string, 0, len(args))
	for _, arg := range args {
		out = append(out, syscall.EscapeArg(arg))
	}
	return strings.Join(out, " "), nil
}

func windowsEnvironmentBlock(env map[string]string) ([]uint16, error) {
	keys := make([]string, 0, len(env))
	for key := range env {
		if strings.TrimSpace(key) == "" {
			continue
		}
		if strings.ContainsRune(key, 0) || strings.ContainsRune(env[key], 0) {
			return nil, errorsNewWindowsProcess("environment contains NUL")
		}
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left := strings.ToUpper(keys[i])
		right := strings.ToUpper(keys[j])
		if left == right {
			return keys[i] < keys[j]
		}
		return left < right
	})
	block := make([]uint16, 0)
	for _, key := range keys {
		entry, err := syscall.UTF16FromString(key + "=" + env[key])
		if err != nil {
			return nil, fmt.Errorf("encode environment variable %s: %w", key, err)
		}
		block = append(block, entry...)
	}
	block = append(block, 0)
	return block, nil
}

func errorsNewWindowsProcess(message string) error {
	return fmt.Errorf("windows sandbox process: %s", message)
}
