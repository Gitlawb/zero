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

func TestDetectShellOutputIssueFlagsMsysCreateFileMappingError(t *testing.T) {
	output := `0 [main] head (3568) C:\Program Files\Git\usr\bin\head.exe: *** fatal error - CreateFileMapping S-1-5-21-3149109338-1484423945-518236903-1001.1, Win32 error 5.  Terminating.`
	issue := detectShellOutputIssue(`git log | head -5`, output, "windows")
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
	issue := detectShellOutputIssue(`"C:\Program Files\Git\usr\bin\head.exe" --version`, output, "windows")
	if issue == nil || issue.Kind != "windows_msys_sandbox" {
		t.Fatalf("expected MSYS output issue, got %#v", issue)
	}
}

func TestDetectShellOutputIssueFlagsMsysTerminatingWithMsysMarker(t *testing.T) {
	output := `1 [main] tail (4321) tail: *** MapViewOfFileEx failed, Win32 error 5.  Terminating.`
	issue := detectShellOutputIssue(`git log | tail -5`, output, "windows")
	if issue == nil || issue.Kind != "windows_msys_sandbox" {
		t.Fatalf("expected MSYS output issue, got %#v", issue)
	}
}

func TestDetectShellOutputIssueIgnoresNonMsysWin32Error5(t *testing.T) {
	output := `myapp.exe: unable to open service handle, Win32 error 5 (access denied). Terminating worker.`
	issue := detectShellOutputIssue(`myapp.exe run`, output, "windows")
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
	if !MsysProneCommandName("HEAD") || MsysProneCommandName("echo") {
		t.Fatalf("unexpected MsysProneCommandName results")
	}
}
