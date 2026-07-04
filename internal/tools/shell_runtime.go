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

var (
	windowsBashStyleCDPattern = regexp.MustCompile(`(?i)(^|[&|;]\s*)cd\s+/(?:[a-ce-z0-9_./~-]|d[a-z0-9_./~-])[a-z0-9_./~-]*`)
	windowsLSCommandPattern   = regexp.MustCompile(`(?i)(^|[&|;]\s*)ls\b(?:\s+|$)`)
	// windowsPosixUtilityPattern catches the other common POSIX coreutils/grep
	// family commands cmd.exe has no equivalent for (unlike `dir`/`findstr`,
	// which sound similar enough that a model might reach for the POSIX name
	// instead) — most often seen piped in, e.g. `git log ... | head`.
	windowsPosixUtilityPattern = regexp.MustCompile(`(?i)(^|[&|;]\s*)(head|tail|grep|wc|awk|sed|cut|xargs|tr)(?:\s+|$)`)
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
		return "Uses " + runtime.Syntax + " syntax on Windows; prefer cwd over cd when changing directories."
	}
	guidance := "Uses " + runtime.Syntax + " syntax."
	if goos == "darwin" {
		// `ps` is setuid root and cannot run under the macOS sandbox; `pgrep` needs a
		// blocked system service. Point the model at the tools that DO work so it
		// doesn't waste turns: lsof to find a process, kill to stop it.
		guidance += " To find or stop a process, use `lsof -i :PORT` (or `lsof -nP -iTCP -sTCP:LISTEN`) for the PID then `kill <pid>`; `ps` and `pgrep` do not work under the sandbox."
	}
	return guidance
}

func detectShellCommandIssue(command string, goos string) *shellIssue {
	if goos != "windows" {
		return nil
	}
	trimmed := strings.TrimSpace(command)
	if windowsBashStyleCDPattern.MatchString(trimmed) ||
		windowsLSCommandPattern.MatchString(trimmed) {
		return &shellIssue{
			Kind:       "windows_shell_syntax",
			Message:    "Command looks like POSIX/Bash syntax, but Zero runs bash tool commands through Windows cmd.exe on this host.",
			Suggestion: "Use the cwd argument instead of cd, use Windows cmd.exe syntax, or use native tools such as list_directory, read_file, grep, and glob.",
		}
	}
	if windowsPosixUtilityPattern.MatchString(trimmed) {
		return &shellIssue{
			Kind:       "windows_shell_syntax",
			Message:    "Command uses a POSIX utility (head/tail/grep/wc/awk/sed/cut/xargs/tr) that Windows cmd.exe does not have.",
			Suggestion: "Use cmd.exe equivalents (e.g. `findstr` for grep, `more` to page output) or Zero's native tools (grep, read_file with offset/limit) instead of piping to a POSIX utility.",
		}
	}
	return nil
}

func detectShellOutputIssue(command string, output string, goos string) *shellIssue {
	if goos != "windows" {
		return nil
	}
	haystack := strings.ToLower(command + "\n" + output)
	if strings.Contains(haystack, "the syntax of the command is incorrect") ||
		strings.Contains(haystack, "is not recognized as an internal or external command") {
		return &shellIssue{
			Kind:       "windows_shell_syntax",
			Message:    "Windows cmd.exe rejected the command syntax.",
			Suggestion: "Translate the command to Windows cmd.exe syntax, set the bash tool cwd argument instead of running cd, or prefer native Zero tools for file inspection.",
		}
	}
	return nil
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
