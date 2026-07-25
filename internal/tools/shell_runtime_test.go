package tools

import (
	"strings"
	"testing"
)

func TestDetectShellCommandIssueFlagsMsysBinaryPaths(t *testing.T) {
	for _, command := range []string{
		`for /F %i in ('whoami') do echo %i | "C:\Program Files\Git\usr\bin\head.exe" -1`,
		`C:\Git\usr\bin\grep.exe pattern file.txt`,
	} {
		issue := detectShellCommandIssue(command, "windows")
		if issue == nil {
			t.Fatalf("expected MSYS path issue for %q", command)
		}
		if issue.Kind != "windows_msys_sandbox" {
			t.Fatalf("expected windows_msys_sandbox kind, got %q", issue.Kind)
		}
		if !strings.Contains(issue.Suggestion, "require_escalated") {
			t.Fatalf("expected escalation guidance, got %#v", issue)
		}
	}
}

func TestDetectShellCommandIssueFlagsStandaloneCat(t *testing.T) {
	issue := detectShellCommandIssue(`cat README.md`, "windows")
	if issue == nil || issue.Kind != "windows_msys_sandbox" {
		t.Fatalf("expected MSYS sandbox issue for cat, got %#v", issue)
	}
}

// TestDetectShellCommandIssueFlagsShells covers bash/sh invocations: every
// executable a bare `bash` resolves to on Windows fails under the restricted
// token (Git-for-Windows MSYS bash dies during runtime init; the System32 WSL
// launcher is denied its WSL service connection), so both names are blocked
// upfront like the MSYS coreutils.
func TestDetectShellCommandIssueFlagsShells(t *testing.T) {
	for _, command := range []string{
		`bash -c "make test"`,
		`bash.exe -lc ls`,
		`sh -c "echo hi"`,
		`sh.exe -c "echo hi"`,
		`git status && bash -c "echo hi"`,
	} {
		issue := detectShellCommandIssue(command, "windows")
		if issue == nil || issue.Kind != "windows_msys_sandbox" {
			t.Fatalf("expected windows_msys_sandbox for %q, got %#v", command, issue)
		}
	}

	// Shell names inside quoted argument text are not invocations.
	for _, command := range []string{
		`git commit -m "bash fails under the sandbox"`,
		`gh pr comment --body "run sh -c manually"`,
	} {
		if issue := detectShellCommandIssue(command, "windows"); issue != nil {
			t.Fatalf("expected quoted shell mention to pass for %q, got %#v", command, issue)
		}
	}
}

func TestDetectShellOutputIssueFlagsMsysCreateFileMappingError(t *testing.T) {
	output := `0 [main] head (3568) C:\Program Files\Git\usr\bin\head.exe: *** fatal error - CreateFileMapping S-1-5-21-3149109338-1484423945-518236903-1001.1, Win32 error 5.  Terminating.`
	issue := detectShellOutputIssue(output, "windows")
	if issue == nil || issue.Kind != "windows_msys_sandbox" {
		t.Fatalf("expected MSYS output issue, got %#v", issue)
	}
	if !strings.Contains(issue.Suggestion, "require_escalated") {
		t.Fatalf("expected escalation guidance, got %#v", issue)
	}
}

func TestDetectShellOutputIssueFlagsMsysSignalPipeError(t *testing.T) {
	output := `0 [main] head (39684) cygheap_user::init: NtSetInformationToken (TokenDefaultDacl), 0xC0000022
648 [main] head (39684) C:\Program Files\Git\usr\bin\head.exe: *** fatal error - couldn't create signal pipe, Win32 error 5`
	issue := detectShellOutputIssue(output, "windows")
	if issue == nil || issue.Kind != "windows_msys_sandbox" {
		t.Fatalf("expected MSYS output issue, got %#v", issue)
	}
}

func TestDetectShellOutputIssueFlagsMsysTerminatingWithMsysMarker(t *testing.T) {
	output := `1 [main] tail (4321) tail: *** MapViewOfFileEx failed, Win32 error 5.  Terminating.`
	issue := detectShellOutputIssue(output, "windows")
	if issue == nil || issue.Kind != "windows_msys_sandbox" {
		t.Fatalf("expected MSYS output issue, got %#v", issue)
	}
}

// TestDetectShellOutputIssueFlagsWslServiceDenied pins detection of the WSL
// bash launcher's failure under the restricted token. The launcher writes
// UTF-16LE to a piped stderr, so the captured text carries a NUL byte after
// every ASCII character; the fixture reproduces that shape.
func TestDetectShellOutputIssueFlagsWslServiceDenied(t *testing.T) {
	var utf16ish strings.Builder
	for _, r := range "Access is denied.\r\nError code: Bash/Service/CreateInstance/E_ACCESSDENIED\r\n" {
		utf16ish.WriteRune(r)
		utf16ish.WriteByte(0)
	}
	issue := detectShellOutputIssue(utf16ish.String(), "windows")
	if issue == nil || issue.Kind != "windows_msys_sandbox" {
		t.Fatalf("expected WSL service-denied output issue, got %#v", issue)
	}
	if !strings.Contains(issue.Message, "WSL") {
		t.Fatalf("expected WSL-specific message, got %#v", issue)
	}
}

func TestDetectShellOutputIssueIgnoresNonMsysWin32Error5(t *testing.T) {
	output := `myapp.exe: unable to open service handle, Win32 error 5 (access denied). Terminating worker.`
	issue := detectShellOutputIssue(output, "windows")
	if issue != nil {
		t.Fatalf("expected no issue for non-MSYS access-denied output, got %#v", issue)
	}
}

func TestShellIssueBlockResultMsysCommand(t *testing.T) {
	result := shellIssueBlockResult(*detectShellCommandIssue(`cat README.md`, "windows"))
	if result.Status != StatusError {
		t.Fatalf("status = %q, want error", result.Status)
	}
	for _, want := range []string{"[zero] shell issue:", "MSYS/Cygwin", "grep", "read_file", "require_escalated"} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("expected %q in blocked output, got %q", want, result.Output)
		}
	}
	if result.Meta["shell_issue"] != "windows_msys_sandbox" {
		t.Fatalf("meta shell_issue = %q", result.Meta["shell_issue"])
	}
}

func TestMsysProneCommandName(t *testing.T) {
	if !MsysProneCommandName("HEAD") || !MsysProneCommandName("bash") || MsysProneCommandName("echo") {
		t.Fatalf("unexpected MsysProneCommandName results")
	}
}

// TestDetectShellCommandIssueFlagsExprAndLsConsistently guards against the
// preflight regex list drifting from the canonical windowsMsysProneNames set
// (both listed expr and ls as MSYS-prone, but the old regex alternations
// omitted expr entirely and let ls hit the older windows_shell_syntax branch
// first, so it never got MSYS-kind guidance).
func TestDetectShellCommandIssueFlagsExprAndLsConsistently(t *testing.T) {
	for _, command := range []string{
		`expr 1 + 1`,
		`expr.exe 1 + 1`,
		`ls -la`,
		`ls`,
	} {
		issue := detectShellCommandIssue(command, "windows")
		if issue == nil || issue.Kind != windowsMsysSandboxKind {
			t.Fatalf("expected windows_msys_sandbox for %q, got %#v", command, issue)
		}
	}
}

// TestDetectShellCommandIssueIgnoresQuotedMsysMentions guards against
// treating an MSYS-prone name that only appears inside a quoted argument
// (e.g. a commit message, a PR comment body, or a doc string discussing the
// command) as an actual invocation. The preflight check must anchor on the
// first word of each command segment, not scan the raw text anywhere.
func TestDetectShellCommandIssueIgnoresQuotedMsysMentions(t *testing.T) {
	for _, command := range []string{
		`git commit -m "fix head.exe crash"`,
		`gh pr comment --body "grep.exe fails under MSYS"`,
		`echo "log | head is broken on windows"`,
		`git commit -m "note: | head does not work here"`,
	} {
		if issue := detectShellCommandIssue(command, "windows"); issue != nil {
			t.Fatalf("expected quoted MSYS mention to pass for %q, got %#v", command, issue)
		}
	}

	// A real invocation alongside quoted text must still be caught.
	if issue := detectShellCommandIssue(`echo "not a real command" && head file.txt`, "windows"); issue == nil || issue.Kind != windowsMsysSandboxKind {
		t.Fatalf("expected real head invocation to still be flagged, got %#v", issue)
	}
}

// TestDetectShellCommandIssueIgnoresQuotedMsysPathMentions guards the
// explicit MSYS-binary-path check the same way as the coreutil-name check:
// a full usr\bin\ path that only appears inside a quoted argument (e.g. a
// commit message describing the failure) must not be treated as an
// invocation, since the path check is now anchored to the first word of each
// command segment rather than scanned across the raw command text.
func TestDetectShellCommandIssueIgnoresQuotedMsysPathMentions(t *testing.T) {
	for _, command := range []string{
		`git commit -m "C:\Program Files\Git\usr\bin\head.exe fails"`,
		`gh pr comment --body "C:\Git\usr\bin\grep.exe is blocked"`,
	} {
		if issue := detectShellCommandIssue(command, "windows"); issue != nil {
			t.Fatalf("expected quoted MSYS path mention to pass for %q, got %#v", command, issue)
		}
	}

	// A real invocation by full path must still be caught.
	if issue := detectShellCommandIssue(`C:\Git\usr\bin\grep.exe pattern file.txt`, "windows"); issue == nil || issue.Kind != windowsMsysSandboxKind {
		t.Fatalf("expected real MSYS path invocation to still be flagged, got %#v", issue)
	}
}

// TestDetectShellCommandIssueRespectsCaretEscapedOperators guards against
// misreading cmd.exe's ^ escape character: `echo ^| head` prints the pipe and
// "head" as literal text (the caret escapes the pipe so it never splits into
// a separate `head` invocation), and `echo foo; head` is a single `echo`
// command with literal arguments since cmd.exe (unlike bash) does not treat
// ; as a statement separator.
func TestDetectShellCommandIssueRespectsCaretEscapedOperators(t *testing.T) {
	for _, command := range []string{
		`echo ^| head`,
		`echo ^& head`,
		`echo foo; head`,
	} {
		if issue := detectShellCommandIssue(command, "windows"); issue != nil {
			t.Fatalf("expected no issue for %q, got %#v", command, issue)
		}
	}

	// An unescaped pipe must still split into a real head invocation.
	if issue := detectShellCommandIssue(`echo foo | head`, "windows"); issue == nil || issue.Kind != windowsMsysSandboxKind {
		t.Fatalf("expected real head invocation to still be flagged, got %#v", issue)
	}
}

// TestDetectShellCommandIssueFlagsRedirectionAttachedToCommand guards against
// firstCommandWord treating cmd.exe redirection operators as part of the
// command name. cmd.exe accepts redirection with no separating space
// (head>out.txt, cat<in.txt), so splitting only on whitespace would return
// "head>out.txt" as one word and miss the invoked command entirely.
func TestDetectShellCommandIssueFlagsRedirectionAttachedToCommand(t *testing.T) {
	for _, command := range []string{
		`some-command | head>out.txt`,
		`cat<in.txt`,
		`grep>matches.txt pattern`,
	} {
		issue := detectShellCommandIssue(command, "windows")
		if issue == nil || issue.Kind != windowsMsysSandboxKind {
			t.Fatalf("expected windows_msys_sandbox for %q, got %#v", command, issue)
		}
	}
}

// TestDetectShellOutputIssueSignatureOmitsCommandText documents, at the type
// level, that detectShellOutputIssue can no longer take the command line as
// evidence: it only accepts the real output. Harmless output must not be
// flagged, and output that genuinely carries the MSYS failure markers must
// still be flagged.
func TestDetectShellOutputIssueSignatureOmitsCommandText(t *testing.T) {
	if issue := detectShellOutputIssue("hello from bash", "windows"); issue != nil {
		t.Fatalf("expected no issue for harmless output, got %#v", issue)
	}
	output := `0 [main] head (3568) C:\Program Files\Git\usr\bin\head.exe: *** fatal error - CreateFileMapping ..., Win32 error 5.  Terminating.`
	if issue := detectShellOutputIssue(output, "windows"); issue == nil || issue.Kind != windowsMsysSandboxKind {
		t.Fatalf("expected real MSYS output to still be flagged, got %#v", issue)
	}
}
