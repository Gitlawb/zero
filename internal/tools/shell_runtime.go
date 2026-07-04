package tools

import (
	"regexp"
	"strings"
)

type shellRuntime struct {
	GOOS       string
	Executable string
	Syntax     string
}

type shellIssue struct {
	Kind       string
	Message    string
	Suggestion string
}

const windowsMsysSandboxSuggestion = "MSYS/Cygwin coreutils from Git for Windows cannot run under Zero's write-restricted Windows sandbox. Prefer Zero native tools (grep, read_file with offset/limit, list_directory, glob), cmd.exe findstr/more, or PowerShell Select-Object -First/-Last. If host-level execution is truly required, rerun with sandbox_permissions: \"require_escalated\" and a narrow justification."

var (
	windowsBashStyleCDPattern = regexp.MustCompile(`(?i)(^|[&|;]\s*)cd\s+/(?:[a-ce-z0-9_./~-]|d[a-z0-9_./~-])[a-z0-9_./~-]*`)
	windowsLSCommandPattern   = regexp.MustCompile(`(?i)(^|[&|;]\s*)ls\b(?:\s+|$)`)
	// windowsMsysBinaryPathPattern catches explicit Git-for-Windows / MSYS usr\bin
	// paths. These executables are valid Windows PE files but fail under the
	// write-restricted sandbox with CreateFileMapping ACCESS_DENIED (#458).
	windowsMsysBinaryPathPattern = regexp.MustCompile(`(?i)(?:\\usr\\bin\\|\\mingw64\\bin\\|msys-2\.0\.dll|cygwin1\.dll)`)
	// windowsMsysPosixExecutablePattern catches quoted or unquoted invocations of
	// MSYS coreutils by executable name (e.g. head.exe), including full paths.
	windowsMsysPosixExecutablePattern = regexp.MustCompile(`(?i)(?:^|[&|;\s"])(?:[\w .:\\-]+\\)?(head|tail|grep|cat|cut|wc|nl|paste|tr|uniq|rev|seq|stat|uname|which|id|awk|sed|xargs)\.exe(?:\s+|$|")`)
	// windowsPosixUtilityPattern catches POSIX coreutils invoked as bare command
	// names (usually via PATH to Git usr\bin). Most often piped, e.g.
	// `git log ... | head`, but also standalone `cat file.txt`.
	windowsPosixUtilityPattern = regexp.MustCompile(`(?i)(^|[&|;]\s*)(head|tail|grep|cat|cut|wc|nl|paste|tr|uniq|rev|seq|stat|uname|which|id|awk|sed|xargs)(?:\s+|$)`)
)

func detectShellRuntime(goos string) shellRuntime {
	if goos == "windows" {
		return shellRuntime{GOOS: goos, Executable: "cmd.exe", Syntax: "Windows cmd.exe"}
	}
	return shellRuntime{GOOS: goos, Executable: "/bin/sh", Syntax: "/bin/sh"}
}

func shellGuidanceForGOOS(goos string) string {
	runtime := detectShellRuntime(goos)
	if goos == "windows" {
		return "Uses " + runtime.Syntax + " syntax on Windows; prefer cwd over cd when changing directories. MSYS/Cygwin coreutils on PATH (Git for Windows usr\\bin) are not sandbox-compatible; prefer native Zero file tools."
	}
	guidance := "Uses " + runtime.Syntax + " syntax."
	if goos == "darwin" {
		guidance += " To find or stop a process, use `lsof -i :PORT` (or `lsof -nP -iTCP -sTCP:LISTEN`) for the PID then `kill <pid>`; `ps` and `pgrep` do not work under the sandbox."
	}
	return guidance
}

// MsysProneCommandName reports whether a bare command name commonly resolves to
// a Git-for-Windows MSYS binary that fails under the Windows restricted sandbox.
func MsysProneCommandName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "cat", "cut", "expr", "grep", "head", "id", "ls", "nl", "paste", "rev",
		"seq", "stat", "tail", "tr", "uname", "uniq", "wc", "which", "awk", "sed", "xargs":
		return true
	default:
		return false
	}
}

func windowsMsysSandboxIssue(message string) *shellIssue {
	return &shellIssue{
		Kind:       "windows_msys_sandbox",
		Message:    message,
		Suggestion: windowsMsysSandboxSuggestion,
	}
}

func detectShellCommandIssue(command string, goos string) *shellIssue {
	if goos != "windows" {
		return nil
	}
	trimmed := strings.TrimSpace(command)
	if windowsMsysBinaryPathPattern.MatchString(trimmed) ||
		windowsMsysPosixExecutablePattern.MatchString(trimmed) {
		return windowsMsysSandboxIssue("Command invokes an MSYS/Cygwin binary path that cannot run under Zero's Windows sandbox.")
	}
	if windowsBashStyleCDPattern.MatchString(trimmed) ||
		windowsLSCommandPattern.MatchString(trimmed) {
		return &shellIssue{
			Kind:       "windows_shell_syntax",
			Message:    "Command looks like POSIX/Bash syntax, but Zero runs bash tool commands through Windows cmd.exe on this host.",
			Suggestion: "Use the cwd argument instead of cd, use Windows cmd.exe syntax, or use native tools such as list_directory, read_file, grep, and glob.",
		}
	}
	if windowsPosixUtilityPattern.MatchString(trimmed) {
		return windowsMsysSandboxIssue("Command uses a POSIX coreutil (head/tail/grep/cat/...) that commonly resolves to Git-for-Windows MSYS binaries incompatible with the Windows sandbox.")
	}
	return nil
}

func detectShellOutputIssue(command string, output string, goos string) *shellIssue {
	if goos != "windows" {
		return nil
	}
	lower := strings.ToLower(command + "\n" + output)
	if msysRuntimeFailedInOutput(lower) {
		return windowsMsysSandboxIssue("An MSYS/Cygwin runtime failed under Zero's Windows sandbox (ACCESS_DENIED during MSYS startup).")
	}
	if strings.Contains(lower, "the syntax of the command is incorrect") ||
		strings.Contains(lower, "is not recognized as an internal or external command") {
		return &shellIssue{
			Kind:       "windows_shell_syntax",
			Message:    "Windows cmd.exe rejected the command syntax.",
			Suggestion: "Translate the command to Windows cmd.exe syntax, set the bash tool cwd argument instead of running cd, or prefer native Zero tools for file inspection.",
		}
	}
	return nil
}

func msysRuntimeFailedInOutput(lower string) bool {
	if strings.Contains(lower, "fatal error - createfilemapping") {
		return true
	}
	if strings.Contains(lower, "couldn't create signal pipe") && strings.Contains(lower, "win32 error 5") {
		return true
	}
	if strings.Contains(lower, "cygheap_user::init") && strings.Contains(lower, "fatal error") {
		return true
	}
	if strings.Contains(lower, "usr\\bin\\") && strings.Contains(lower, "fatal error") {
		return true
	}
	if !strings.Contains(lower, "win32 error 5") || !strings.Contains(lower, "terminating") {
		return false
	}
	// Anchor the broad win32-error-5 fallback to an MSYS-specific marker so
	// unrelated access-denied failures are not mislabeled as MSYS sandbox
	// incompatibilities.
	return strings.Contains(lower, `usr\bin\`) ||
		strings.Contains(lower, "cygheap") ||
		strings.Contains(lower, "msys-2.0.dll") ||
		strings.Contains(lower, "cygwin1.dll") ||
		strings.Contains(lower, "[main]")
}

func appendShellIssueHint(output string, issue shellIssue) string {
	output = strings.TrimRight(output, "\r\n")
	hint := "[zero] shell issue: " + issue.Message
	if strings.TrimSpace(issue.Suggestion) != "" {
		hint += "\nSuggestion: " + issue.Suggestion
	}
	if output == "" {
		return hint
	}
	return output + "\n" + hint
}
