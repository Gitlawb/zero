package tools

import (
	"regexp"
	"strings"
	"unicode"
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

const windowsMsysSandboxKind = "windows_msys_sandbox"

const windowsMsysSandboxSuggestion = "MSYS/Cygwin coreutils from Git for Windows cannot run under Zero's write-restricted Windows sandbox. Prefer Zero native tools (grep, read_file with offset/limit, list_directory, glob), cmd.exe findstr, or PowerShell Select-Object -First/-Last. If host-level execution is truly required, rerun with sandbox_permissions: \"require_escalated\" and a narrow justification."

// windowsMsysProneNames is the single source of truth for POSIX coreutil names
// that commonly resolve to a Git-for-Windows MSYS/Cygwin binary rather than a
// cmd.exe-native command, and so fail under the write-restricted Windows
// sandbox (#458). Every Windows MSYS-detection path (the preflight command
// scan below, the exported MsysProneCommandName, and the known-safe-segment
// guard in internal/agent/command_prefix.go) derives from this one set, so
// they cannot drift out of sync with each other.
var windowsMsysProneNames = map[string]bool{
	"cat": true, "cut": true, "expr": true, "grep": true, "head": true,
	"id": true, "ls": true, "nl": true, "paste": true, "rev": true,
	"seq": true, "stat": true, "tail": true, "tr": true, "uname": true,
	"uniq": true, "wc": true, "which": true, "awk": true, "sed": true,
	"xargs": true,
}

var (
	windowsBashStyleCDPattern = regexp.MustCompile(`(?i)(^|[&|;]\s*)cd\s+/(?:[a-ce-z0-9_./~-]|d[a-z0-9_./~-])[a-z0-9_./~-]*`)
	// windowsMsysBinaryPathPattern catches explicit Git-for-Windows / MSYS usr\bin
	// paths. These executables are valid Windows PE files but fail under the
	// write-restricted sandbox with CreateFileMapping ACCESS_DENIED (#458).
	windowsMsysBinaryPathPattern = regexp.MustCompile(`(?i)(?:\\usr\\bin\\|\\mingw64\\bin\\|msys-2\.0\.dll|cygwin1\.dll)`)
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
	return windowsMsysProneNames[strings.ToLower(strings.TrimSpace(name))]
}

func windowsMsysSandboxIssue(message string) *shellIssue {
	return &shellIssue{
		Kind:       windowsMsysSandboxKind,
		Message:    message,
		Suggestion: windowsMsysSandboxSuggestion,
	}
}

// windowsCommandSegments splits a command into cmd.exe-operator-separated
// segments (&, |, and their doubled forms &&/||), respecting double quotes
// (cmd.exe's grouping construct) and the caret (^) escape character, so an
// operator or command name mentioned inside a quoted argument (e.g. a commit
// message or PR comment body), or an operator escaped with ^ (cmd.exe prints
// `echo ^| head` as literal text instead of piping to head), is not mistaken
// for a real segment boundary or invocation. Unlike bash, cmd.exe does not
// treat ; as a statement separator, so it is left as ordinary argument text
// (e.g. `echo foo; head` is a single `echo` invocation with literal args).
func windowsCommandSegments(command string) []string {
	var segments []string
	var current strings.Builder
	inQuotes := false
	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if !inQuotes && c == '^' && i+1 < len(runes) {
			current.WriteRune(c)
			i++
			current.WriteRune(runes[i])
			continue
		}
		if c == '"' {
			inQuotes = !inQuotes
			current.WriteRune(c)
			continue
		}
		if !inQuotes && (c == '&' || c == '|') {
			if seg := strings.TrimSpace(current.String()); seg != "" {
				segments = append(segments, seg)
			}
			current.Reset()
			continue
		}
		current.WriteRune(c)
	}
	if seg := strings.TrimSpace(current.String()); seg != "" {
		segments = append(segments, seg)
	}
	return segments
}

// firstCommandWord returns the first token of a command segment. A leading
// double-quoted span counts as one token with its quotes stripped, since
// cmd.exe treats a quoted path as a single argument: the command invoked by
// `"C:\Program Files\Git\usr\bin\head.exe" file.txt` is the quoted path, not
// "C:\Program. For an unquoted command, the token ends at whitespace or a
// redirection operator (<, >): cmd.exe accepts redirection attached directly
// to the command name with no separating space (head>out.txt, cat<in.txt), so
// stopping only at whitespace would return "head>out.txt" as one word and
// miss the invoked command.
func firstCommandWord(segment string) string {
	trimmed := strings.TrimSpace(segment)
	if trimmed == "" {
		return ""
	}
	if trimmed[0] == '"' {
		if end := strings.IndexByte(trimmed[1:], '"'); end >= 0 {
			return trimmed[1 : end+1]
		}
		return trimmed[1:]
	}
	if end := strings.IndexFunc(trimmed, isCommandWordBoundary); end >= 0 {
		return trimmed[:end]
	}
	return trimmed
}

func isCommandWordBoundary(r rune) bool {
	return unicode.IsSpace(r) || r == '<' || r == '>'
}

// msysProneCommandWord reports whether word (the first token of a command
// segment, as returned by firstCommandWord) names an MSYS-prone coreutil,
// bare or with a directory prefix and/or .exe suffix (head, head.exe,
// C:\...\usr\bin\head.exe, ...).
func msysProneCommandWord(word string) bool {
	word = strings.Trim(word, `"`)
	if i := strings.LastIndexAny(word, `\/`); i >= 0 {
		word = word[i+1:]
	}
	word = strings.TrimSuffix(strings.ToLower(word), ".exe")
	return MsysProneCommandName(word)
}

func detectShellCommandIssue(command string, goos string) *shellIssue {
	if goos != "windows" {
		return nil
	}
	trimmed := strings.TrimSpace(command)
	// Blank out double-quoted spans before matching, so a `cd /foo`-shaped
	// string that only appears inside a quoted argument value (e.g. a `gh`
	// or `git commit` message) is not mistaken for an actual command cmd.exe
	// would interpret. Then neutralize cmd.exe caret escapes of its own
	// metacharacters (foo^|cat is the literal token foo|cat, not a pipe into
	// cat), so an escaped metachar can't stand in as a fake segment boundary
	// either.
	unquoted := stripCmdCaretEscapes(stripDoubleQuotedSpans(trimmed))
	if windowsBashStyleCDPattern.MatchString(unquoted) {
		return &shellIssue{
			Kind:       "windows_shell_syntax",
			Message:    "Command looks like POSIX/Bash syntax, but Zero runs bash tool commands through Windows cmd.exe on this host.",
			Suggestion: "Use the cwd argument instead of cd, use Windows cmd.exe syntax, or use native tools such as list_directory, read_file, grep, and glob.",
		}
	}
	// Check the first word of each operator-separated segment (not the raw
	// text anywhere in the command) against the MSYS binary-path pattern and
	// the single MSYS-prone name set, covering bare names (head), .exe names
	// (head.exe), and directory-prefixed forms (C:\...\head.exe) uniformly.
	// Being segment/word anchored rather than a whole-string regex or scan,
	// neither check matches text that only appears inside a quoted argument
	// (e.g. a commit message or PR comment body discussing head.exe).
	for _, segment := range windowsCommandSegments(trimmed) {
		word := firstCommandWord(segment)
		if windowsMsysBinaryPathPattern.MatchString(word) {
			return windowsMsysSandboxIssue("Command invokes an MSYS/Cygwin binary path that cannot run under Zero's Windows sandbox.")
		}
		if msysProneCommandWord(word) {
			return windowsMsysSandboxIssue("Command uses a POSIX coreutil (head/tail/grep/cat/...) that commonly resolves to Git-for-Windows MSYS binaries incompatible with the Windows sandbox.")
		}
	}
	return nil
}

// stripDoubleQuotedSpans replaces the contents of every double-quoted span
// (quotes included) with spaces, preserving the string's length and the
// position of unquoted text. cmd.exe treats a double-quoted span as a single
// literal token, so operators/utility names inside one are not real command
// syntax; blanking them out keeps the Windows command-issue regexes anchored
// to text cmd.exe would actually interpret.
func stripDoubleQuotedSpans(command string) string {
	var b strings.Builder
	b.Grow(len(command))
	inQuotes := false
	for _, c := range command {
		if c == '"' {
			inQuotes = !inQuotes
			b.WriteByte(' ')
			continue
		}
		if inQuotes {
			b.WriteByte(' ')
			continue
		}
		b.WriteRune(c)
	}
	return b.String()
}

// cmdEscapedMetacharPattern matches a cmd.exe caret escape of one of its own
// metacharacters. cmd.exe treats the escaped character as a literal, not an
// operator, so e.g. `foo^|cat` is the single literal token foo|cat, not a
// pipe into cat.
var cmdEscapedMetacharPattern = regexp.MustCompile(`\^[&|;^<>]`)

// stripCmdCaretEscapes blanks out cmd.exe caret-escape sequences (both
// characters), so an escaped metacharacter cannot be mistaken by the Windows
// command-issue regexes for a real operator/segment boundary.
func stripCmdCaretEscapes(command string) string {
	return cmdEscapedMetacharPattern.ReplaceAllString(command, "  ")
}

// detectShellOutputIssue looks for MSYS runtime crash markers and cmd.exe
// syntax-error text in output only, never in the command that was run. The
// command line is attacker/user-controlled argument text (e.g. a `gh pr
// comment --body` quoting a sample failure), not something the shell
// produced, so treating it as evidence would reintroduce the same
// quoted-text false positives the preflight command-position check exists to
// avoid, just after execution instead of before it.
func detectShellOutputIssue(output string, goos string) *shellIssue {
	if goos != "windows" {
		return nil
	}
	lower := strings.ToLower(output)
	if msysRuntimeFailedInOutput(lower) {
		return windowsMsysSandboxIssue("An MSYS/Cygwin runtime failed under Zero's Windows sandbox (ACCESS_DENIED during MSYS startup).")
	}
	if strings.Contains(lower, "the syntax of the command is incorrect") ||
		strings.Contains(lower, "is not recognized as an internal or external command") {
		return &shellIssue{
			Kind:       "windows_shell_syntax",
			Message:    "Windows cmd.exe rejected the command syntax.",
			Suggestion: "Use Windows cmd.exe syntax. Quote args with | using double quotes (e.g. --jq \".a | b\"). Avoid | head; use --jq or PowerShell Select-Object -First N instead. Prefer native tools.",
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
